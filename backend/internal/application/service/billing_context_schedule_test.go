//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveContextPricingScheduleOpenAITiers(t *testing.T) {
	svc := newTestBillingService()
	resolver := NewModelPricingResolver(nil, svc)

	sched, err := svc.ResolveContextPricingSchedule(context.Background(), resolver, ContextPricingScheduleInput{Model: "gpt-6-luna"})
	require.NoError(t, err)
	require.NotNil(t, sched)
	require.Equal(t, ContextPricingBasisWholeRequest, sched.Basis)
	require.Len(t, sched.Tiers, 2)

	// 标准档：目录价，12h 写入按默认策略 = 输入价 × 2.5。
	base := sched.Tiers[0]
	require.Equal(t, 0, base.MinTokens)
	require.NotNil(t, base.MaxTokens)
	require.Equal(t, "≤272K", base.Label)
	require.InDelta(t, 0.1e-6, *base.Input, 1e-15)
	require.InDelta(t, 0.5e-6, *base.Output, 1e-15)
	require.NotNil(t, base.CacheWrite12h)
	require.InDelta(t, 0.25e-6, *base.CacheWrite12h, 1e-15)

	// 长上下文档：输入 ×2、输出 ×1.5，12h 写入同步缩放。
	long := sched.Tiers[1]
	require.Equal(t, ">272K", long.Label)
	require.InDelta(t, 0.2e-6, *long.Input, 1e-15)
	require.InDelta(t, 0.75e-6, *long.Output, 1e-15)
	require.NotNil(t, long.CacheWrite12h)
	require.InDelta(t, 0.5e-6, *long.CacheWrite12h, 1e-15)
}

func TestResolveContextPricingScheduleFable51SingleTier(t *testing.T) {
	svc := newTestBillingService()
	resolver := NewModelPricingResolver(nil, svc)

	sched, err := svc.ResolveContextPricingSchedule(context.Background(), resolver, ContextPricingScheduleInput{Model: "claude-fable-5-1"})
	require.NoError(t, err)
	require.NotNil(t, sched)
	require.Len(t, sched.Tiers, 1, "无长上下文规则的模型只有单档")

	tier := sched.Tiers[0]
	require.InDelta(t, 10e-6, *tier.Input, 1e-15)
	require.InDelta(t, 50e-6, *tier.Output, 1e-15)
	require.NotNil(t, tier.CacheWrite)
	require.InDelta(t, 12.5e-6, *tier.CacheWrite, 1e-15)
	require.NotNil(t, tier.CacheWrite1h)
	require.InDelta(t, 20e-6, *tier.CacheWrite1h, 1e-15)
	require.Nil(t, tier.CacheWrite12h, "Anthropic 模型不产生 12h 桶")
}

func TestResolvedTimePricingScheduleKeepsWeekdaysOnly(t *testing.T) {
	resolved := &ResolvedPricing{
		Source: PricingSourceChannel,
		channelPricing: &ChannelModelPricing{
			TimePricing: &ChannelTimePricing{
				Timezone:     "Asia/Shanghai",
				WeekdaysOnly: true,
				Periods: []ChannelTimePricingPeriod{
					{StartTime: "00:30", EndTime: "08:30", Multiplier: 0.5},
				},
			},
		},
	}

	sched := resolvedTimePricingSchedule(resolved)
	require.NotNil(t, sched, "探针锚点必须落在工作日，否则仅工作日配置的时段会被整组剔除")
	require.True(t, sched.WeekdaysOnly)
	require.Equal(t, []TimePricingPeriod{
		{StartTime: "00:30", EndTime: "08:30", Multiplier: 0.5},
	}, sched.Periods)
}

func TestResolveContextPricingScheduleRequiresResolver(t *testing.T) {
	svc := newTestBillingService()
	_, err := svc.ResolveContextPricingSchedule(context.Background(), nil, ContextPricingScheduleInput{Model: "gpt-6-luna"})
	require.ErrorIs(t, err, errContextPricingResolverRequired)
}

func TestResolveContextPricingScheduleNonTokenModeIsNil(t *testing.T) {
	svc := newTestBillingService()
	group := &Group{ID: 7, Platform: PlatformOpenAI, ModelPricing: []ChannelModelPricing{
		{Models: []string{"gpt-image-2"}, BillingMode: BillingModeImage, PerRequestPrice: floatPtr(0.02)},
	}}
	group.compileModelPricingIndex()
	resolver := NewModelPricingResolver(nil, svc)

	sched, err := svc.ResolveContextPricingSchedule(context.Background(), resolver, ContextPricingScheduleInput{
		Model: "gpt-image-2",
		Group: group,
	})
	require.NoError(t, err)
	require.Nil(t, sched, "非 token 计费模式不产生上下文阶梯")
}

func TestPlazaOfficialPricingIncludesLongContextIntervals(t *testing.T) {
	pricingSvc := newStubPricingServiceFromMap(map[string]*LiteLLMModelPricing{
		"gpt-6-luna": {
			Mode:                        "chat",
			InputCostPerToken:           0.1e-6,
			OutputCostPerToken:          0.5e-6,
			CacheCreationInputTokenCost: 0.125e-6,
			CacheReadInputTokenCost:     0.01e-6,
		},
	})
	channels := []Channel{plazaPricedChannel(1, "ch", []int64{10}, PlatformOpenAI, "gpt-6-luna")}
	groups := []Group{{ID: 10, Name: "g", Platform: PlatformOpenAI, RateMultiplier: 1}}
	svc := newPlazaChannelService(channels, groups, pricingSvc)

	out, err := svc.ListPlazaGroups(context.Background(), PlazaListOptions{})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 1)

	official := out[0].Models[0].OfficialPricing
	require.NotNil(t, official)
	require.Len(t, official.Intervals, 2, "官方长上下文阶梯应作为区间下发")
	require.Equal(t, "≤272K", official.Intervals[0].TierLabel)
	require.Equal(t, ">272K", official.Intervals[1].TierLabel)
	require.InDelta(t, 0.1e-6, *official.Intervals[0].InputPrice, 1e-15)
	require.InDelta(t, 0.2e-6, *official.Intervals[1].InputPrice, 1e-15)
	require.InDelta(t, 0.25e-6, *official.Intervals[0].CacheWrite12hPrice, 1e-15)
	require.InDelta(t, 0.5e-6, *official.Intervals[1].CacheWrite12hPrice, 1e-15)
}
