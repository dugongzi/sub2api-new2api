package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelTimePricingMultiplierAtUsesTimezoneAndHalfOpenPeriods(t *testing.T) {
	config := &ChannelTimePricing{
		Timezone: "Asia/Shanghai",
		Periods: []ChannelTimePricingPeriod{
			{StartTime: "09:00", EndTime: "18:00", Multiplier: 1.5},
		},
	}
	require.NoError(t, ValidateChannelTimePricing(config))

	inside := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	boundary := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	require.InDelta(t, 1.5, config.MultiplierAt(inside), 1e-9)
	require.InDelta(t, 1.0, config.MultiplierAt(boundary), 1e-9)
}

func TestChannelTimePricingMultiplierAtWeekdaysOnly(t *testing.T) {
	config := &ChannelTimePricing{
		Timezone:     "Asia/Shanghai",
		WeekdaysOnly: true,
		Periods: []ChannelTimePricingPeriod{
			{StartTime: "09:00", EndTime: "18:00", Multiplier: 2},
		},
	}
	everyDay := &ChannelTimePricing{
		Timezone: "Asia/Shanghai",
		Periods: []ChannelTimePricingPeriod{
			{StartTime: "09:00", EndTime: "18:00", Multiplier: 2},
		},
	}

	tests := []struct {
		name string
		at   time.Time
		want float64
	}{
		{name: "Monday in configured timezone", at: time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC), want: 2},
		{name: "Saturday in configured timezone", at: time.Date(2026, 7, 4, 1, 0, 0, 0, time.UTC), want: 1},
		{name: "Sunday in configured timezone", at: time.Date(2026, 7, 5, 1, 0, 0, 0, time.UTC), want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, config.MultiplierAt(tt.at))
			// 相同时段配置下，weekdays_only 不能污染每日生效的编译缓存。
			require.Equal(t, 2.0, everyDay.MultiplierAt(tt.at))
		})
	}
}

func TestChannelTimePricingRejectsOverlapAndInvalidTimezone(t *testing.T) {
	require.Error(t, ValidateChannelTimePricing(&ChannelTimePricing{
		Timezone: "Asia/Shanghai",
		Periods: []ChannelTimePricingPeriod{
			{StartTime: "09:00", EndTime: "12:00", Multiplier: 1.2},
			{StartTime: "11:00", EndTime: "13:00", Multiplier: 1.3},
		},
	}))
	require.Error(t, ValidateChannelTimePricing(&ChannelTimePricing{
		Timezone: "not/a/timezone",
		Periods:  []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "10:00", Multiplier: 1.2}},
	}))
}

func TestBillingAppliesChannelTimeMultiplierToAllTokenCostComponents(t *testing.T) {
	input := &CostBreakdown{
		InputCost: 1, OutputCost: 2, CacheCreationCost: 3, CacheReadCost: 4,
		TotalCost: 10, ActualCost: 10,
	}
	applyCostBreakdownMultiplier(input, 1.5)
	require.Equal(t, 1.5, input.InputCost)
	require.Equal(t, 3.0, input.OutputCost)
	require.Equal(t, 4.5, input.CacheCreationCost)
	require.Equal(t, 6.0, input.CacheReadCost)
	require.Equal(t, 15.0, input.TotalCost)
	require.Equal(t, 15.0, input.ActualCost)
}
