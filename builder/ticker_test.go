package builder

import (
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/beacon"
	boostTypes "github.com/flashbots/go-boost-utils/types"
	"github.com/stretchr/testify/require"
)

type mockBuildBlockHook struct {
	t  *testing.T
	mu sync.Mutex

	count                      int
	lastBuiltPayloadAttributes *beacon.PayloadAttributesV1
	lastBuildHeadHash          common.Hash
	ticker                     *SlotTicker
}

func newMockBuildBlockHook(t *testing.T, ticker *SlotTicker) *mockBuildBlockHook {
	return &mockBuildBlockHook{
		t:      t,
		ticker: ticker,
	}
}

func (h *mockBuildBlockHook) GetRequestCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

func (h *mockBuildBlockHook) BuildBlock(payloadAttributes *beacon.PayloadAttributesV1, headBlockHash common.Hash) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastBuiltPayloadAttributes = payloadAttributes
	h.lastBuildHeadHash = headBlockHash
	h.count++
	h.ticker.c <- &BlockBuilderArgs{PayloadAttributes: payloadAttributes, HeadBlockHash: headBlockHash}
	return nil
}

func TestTicker(t *testing.T) {
	feeRecipient, _ := boostTypes.HexToAddress("0xabcf8e0d4e9587369b2301d0790347320302cc00")
	testRelay := testRelay{
		validator: ValidatorData{
			Pubkey:       PubkeyHex("0xb67d2c11bcab8c4394fc2faa9601d0b99c7f4b37e14911101da7d97077917862eed4563203d34b91b5cf0aa44d6cfa05"),
			FeeRecipient: feeRecipient,
			GasLimit:     10,
			Timestamp:    15,
		},
	}
	testPayloadAttributes := &beacon.PayloadAttributesV1{
		Timestamp:             15,
		Random:                common.Hash{0x05, 0x10},
		SuggestedFeeRecipient: common.Address{0x04, 0x10},
		GasLimit:              uint64(21),
		Slot:                  25,
	}
	testHeadHash := common.Hash{0x05, 0x10}
	getCurrTimestamp := func() uint64 { return uint64(time.Now().Unix()) }

	t.Run("it should do nothing if fcU is not set", func(t *testing.T) {
		ticker := NewSlotTicker(nil, &testRelay)
		mockBuildBlockHook := newMockBuildBlockHook(t, ticker)
		ticker.buildBlockHook = mockBuildBlockHook.BuildBlock

		require.NoError(t, ticker.Start(1, 0))
		time.Sleep(time.Second)
		require.Equal(t, 0, mockBuildBlockHook.GetRequestCount())
	})

	t.Run("it should build the next slot if timeout is hit", func(t *testing.T) {
		ticker := NewSlotTicker(nil, &testRelay)
		mockBuildBlockHook := newMockBuildBlockHook(t, ticker)
		ticker.buildBlockHook = mockBuildBlockHook.BuildBlock

		require.NoError(t, ticker.Start(2, 1))

		startSlotTime := getCurrTimestamp()
		startSlot := uint64(25)
		testPayloadAttributes.Timestamp = startSlotTime
		testPayloadAttributes.Slot = startSlot

		ticker.c <- &BlockBuilderArgs{PayloadAttributes: testPayloadAttributes, HeadBlockHash: testHeadHash}
		// should trigger a block build after slot + offset
		time.Sleep(3 * time.Second)
		// after a build is triggered the payload should be for the next slot
		require.Equal(t, 1, mockBuildBlockHook.GetRequestCount())
		require.Equal(t, startSlotTime+2, mockBuildBlockHook.lastBuiltPayloadAttributes.Timestamp)
		require.Equal(t, startSlot+1, mockBuildBlockHook.lastBuiltPayloadAttributes.Slot)
		require.Equal(t, testHeadHash, mockBuildBlockHook.lastBuildHeadHash)
	})

	t.Run("it should reset timeout when fcU is received", func(t *testing.T) {
		ticker := NewSlotTicker(nil, &testRelay)
		mockBuildBlockHook := newMockBuildBlockHook(t, ticker)
		ticker.buildBlockHook = mockBuildBlockHook.BuildBlock

		require.NoError(t, ticker.Start(2, 1))

		startSlotTime := getCurrTimestamp()
		startSlot := uint64(25)
		testPayloadAttributes.Timestamp = startSlotTime
		testPayloadAttributes.Slot = startSlot

		ticker.c <- &BlockBuilderArgs{PayloadAttributes: testPayloadAttributes, HeadBlockHash: testHeadHash}
		time.Sleep(1 * time.Second)
		// fcu before timeout should reset the timer
		nextSlotTime := getCurrTimestamp()
		testPayloadAttributes.Timestamp = nextSlotTime
		testPayloadAttributes.Slot = startSlot + 1
		ticker.c <- &BlockBuilderArgs{PayloadAttributes: testPayloadAttributes, HeadBlockHash: testHeadHash}
		time.Sleep(2 * time.Second)
		require.Equal(t, 0, mockBuildBlockHook.GetRequestCount())

		// build should be triggered after 2 + 1 seconds and the payload should be for the next slot
		time.Sleep(1 * time.Second)
		require.Equal(t, 1, mockBuildBlockHook.GetRequestCount())
		require.Equal(t, nextSlotTime+2, mockBuildBlockHook.lastBuiltPayloadAttributes.Timestamp)
		require.Equal(t, startSlot+2, mockBuildBlockHook.lastBuiltPayloadAttributes.Slot)
		require.Equal(t, testHeadHash, mockBuildBlockHook.lastBuildHeadHash)
	})

	t.Run("it should continue to build multiple empty slots", func(t *testing.T) {
		ticker := NewSlotTicker(nil, &testRelay)
		mockBuildBlockHook := newMockBuildBlockHook(t, ticker)
		ticker.buildBlockHook = mockBuildBlockHook.BuildBlock

		require.NoError(t, ticker.Start(2, 1))

		startSlotTime := getCurrTimestamp()
		startSlot := uint64(25)
		testPayloadAttributes.Timestamp = startSlotTime
		testPayloadAttributes.Slot = startSlot

		ticker.c <- &BlockBuilderArgs{PayloadAttributes: testPayloadAttributes, HeadBlockHash: testHeadHash}
		time.Sleep(6 * time.Second)

		// 3 blocks should be built
		require.Equal(t, 3, mockBuildBlockHook.GetRequestCount())
		require.Equal(t, startSlotTime+6, mockBuildBlockHook.lastBuiltPayloadAttributes.Timestamp)
		require.Equal(t, startSlot+3, mockBuildBlockHook.lastBuiltPayloadAttributes.Slot)
		require.Equal(t, testHeadHash, mockBuildBlockHook.lastBuildHeadHash)
	})
}
