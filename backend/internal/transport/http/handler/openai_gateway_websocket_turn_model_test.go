package handler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DR-lin-eng/sub2api-no2api/internal/application/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// A20：passthrough 首轮不会调用 BeforeTurn，因此没有 freeze 时必须回退到
// relay 报告的本轮起点，而不是零值。
func TestOpenAIWSTurnPricingCurrentOrFallsBackToTurnStart(t *testing.T) {
	turnStart := time.Date(2026, time.November, 21, 10, 0, 0, 0, time.UTC)

	var pricing openAIWSTurnPricing
	require.Equal(t, turnStart, pricing.currentOr(turnStart))

	frozen := turnStart.Add(30 * time.Second)
	pricing.freeze(frozen)
	require.Equal(t, frozen, pricing.currentOr(turnStart))
}

func TestOpenAIWSTurnChannelMappingStateKeepsExactTurnSnapshot(t *testing.T) {
	var state openAIWSTurnChannelMappingState
	first := service.ChannelMappingResult{Mapped: true, MappedModel: "gpt-5.6-sol", ChannelID: 1}
	second := service.ChannelMappingResult{Mapped: true, MappedModel: "gpt-5.6-terra", ChannelID: 2}

	state.Store(1, "sol", first)
	got, ok := state.Load(1)
	require.True(t, ok)
	require.Equal(t, "sol", got.requestedModel)
	require.Equal(t, first, got.mapping)

	state.Store(2, "terra", second)
	_, oldTurnExists := state.Load(1)
	require.False(t, oldTurnExists)
	got, ok = state.Load(2)
	require.True(t, ok)
	require.Equal(t, "terra", got.requestedModel)
	require.Equal(t, second, got.mapping)
}

func BenchmarkOpenAIWSTurnChannelMappingHotPath(b *testing.B) {
	var state openAIWSTurnChannelMappingState
	mapping := service.ChannelMappingResult{
		Mapped:             true,
		MappedModel:        "gpt-5.6-sol",
		ChannelID:          42,
		BillingModelSource: service.BillingModelSourceChannelMapped,
	}
	b.ReportAllocs()
	for turn := 1; turn <= b.N; turn++ {
		state.Store(turn, "public-sol", mapping)
		snapshot, ok := state.Load(turn)
		if !ok || snapshot.mapping.MappedModel != mapping.MappedModel {
			b.Fatal("turn snapshot was not preserved")
		}
	}
}

func TestShouldReportOpenAIWSProxyAccountFailureIgnoresClientClose(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "bare normal close", err: coderws.CloseError{Code: coderws.StatusNormalClosure, Reason: "done"}, want: false},
		{name: "wrapped normal close", err: fmt.Errorf("read: %w", coderws.CloseError{Code: coderws.StatusNormalClosure}), want: false},
		{name: "client cancellation", err: service.NewOpenAIWSClientCloseError(coderws.StatusGoingAway, "request canceled", context.Canceled), want: false},
		{name: "abnormal close", err: coderws.CloseError{Code: coderws.StatusAbnormalClosure, Reason: "reset"}, want: true},
		{name: "upstream failure", err: errors.New("upstream websocket read failed"), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldReportOpenAIWSProxyAccountFailure(tt.err))
		})
	}
}
