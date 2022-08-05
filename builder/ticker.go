package builder

import (
	"errors"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/beacon"
	"github.com/ethereum/go-ethereum/log"
)

type BlockBuilderArgs struct {
	PayloadAttributes *beacon.PayloadAttributesV1
	HeadBlockHash     common.Hash
}

type SlotTicker struct {
	c    chan *BlockBuilderArgs
	done chan struct{}

	buildBlockHook func(payloadAttributes *beacon.PayloadAttributesV1, headBlockHash common.Hash) error
	relay          IRelay
}

func (s *SlotTicker) C() <-chan *BlockBuilderArgs {
	return s.c
}

func (s *SlotTicker) Done() {
	go func() {
		s.done <- struct{}{}
	}()
}

func NewSlotTicker(
	buildBlockHook func(payloadAttributes *beacon.PayloadAttributesV1, headBlockHash common.Hash) error,
	relay IRelay,
) *SlotTicker {
	ticker := &SlotTicker{
		c:    make(chan *BlockBuilderArgs, 1),
		done: make(chan struct{}),

		buildBlockHook: buildBlockHook,
		relay:          relay,
	}
	return ticker
}

func (s *SlotTicker) Start(
	secondsPerSlot uint64,
	offset time.Duration,
) error {
	if offset > time.Duration(secondsPerSlot)*time.Second {
		return errors.New("invalid ticker offset")
	}

	s.start(secondsPerSlot, offset)
	return nil
}

func (s *SlotTicker) start(
	secondsPerSlot uint64,
	offset time.Duration,
) {
	d := time.Duration(secondsPerSlot) * time.Second
	t := time.NewTimer(d + offset)
	var payloadAttributes *beacon.PayloadAttributesV1
	var headBlockHash common.Hash

	var mu sync.Mutex

	go func() {
		for {
			select {
			case blockBuilderArgs := <-s.c: // received fcU from beacon node
				if blockBuilderArgs == nil || blockBuilderArgs.PayloadAttributes == nil {
					continue
				}

				mu.Lock()

				log.Info("received fcU from beacon node", "payloadAttributes",
					blockBuilderArgs.PayloadAttributes, "headBlockHash", blockBuilderArgs.HeadBlockHash)
				// first fcU, assign initial payload and head block hash
				if payloadAttributes == nil {
					payloadAttributes = blockBuilderArgs.PayloadAttributes
				}

				// modify payload attributes and header block hash for next slot
				headBlockHash = blockBuilderArgs.HeadBlockHash

				// timer to trigger block building for next slot timestamp + 12 (slot time) + 4
				timeSlotDelta := time.Until(time.Unix(int64(payloadAttributes.Timestamp), 0))
				timeUntilNextSlot := d + offset + timeSlotDelta

				nextSlot := blockBuilderArgs.PayloadAttributes.Slot + 1
				nextTimestamp := blockBuilderArgs.PayloadAttributes.Timestamp + uint64(d.Seconds())

				if vd, err := s.relay.GetValidatorForSlot(nextSlot); err == nil {
					blockBuilderArgs.PayloadAttributes.SuggestedFeeRecipient = [20]byte(vd.FeeRecipient)
					blockBuilderArgs.PayloadAttributes.GasLimit = vd.GasLimit
				}

				payloadAttributes = &beacon.PayloadAttributesV1{
					Slot:                  nextSlot,
					Timestamp:             nextTimestamp,
					SuggestedFeeRecipient: blockBuilderArgs.PayloadAttributes.SuggestedFeeRecipient,
					GasLimit:              blockBuilderArgs.PayloadAttributes.GasLimit,
					Random:                blockBuilderArgs.PayloadAttributes.Random,
				}

				t.Reset(timeUntilNextSlot)

				mu.Unlock()
			case <-t.C: // missed slot, force build a block
				if payloadAttributes != nil {
					log.Info("missed slot, forcing building a block", "payloadAttributes", payloadAttributes,
						"headBlockHash", headBlockHash)
					s.buildBlockHook(payloadAttributes, headBlockHash)
				}
			case <-s.done:
				close(s.c)
				return
			}
		}
	}()
}
