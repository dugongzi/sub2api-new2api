//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func floatPtr(v float64) *float64 {
	return &v
}

func TestComputeCacheCreationCostAnthropicBreakdown(t *testing.T) {
	svc := newTestBillingService()
	tokens := UsageTokens{
		CacheCreationTokens:   100,
		CacheCreation5mTokens: 40,
		CacheCreation1hTokens: 60,
	}
	pricing := &ModelPricing{
		CacheCreation5mPrice:   1e-6,
		CacheCreation1hPrice:   2e-6,
		SupportsCacheBreakdown: true,
	}
	cost := svc.computeCacheCreationCost(pricing, tokens, 1e-6, 1)
	require.InDelta(t, 40e-6+120e-6, cost, 1e-12)
}

func TestComputeCacheCreationCostAnthropicBreakdownFallsBackTo5m(t *testing.T) {
	svc := newTestBillingService()
	pricing := &ModelPricing{
		CacheCreation5mPrice:   1e-6,
		CacheCreation1hPrice:   2e-6,
		SupportsCacheBreakdown: true,
	}
	// 上游只给聚合值，没有 ephemeral 明细：全部按 5m 计价。
	cost := svc.computeCacheCreationCost(pricing, UsageTokens{CacheCreationTokens: 100}, 1e-6, 1)
	require.InDelta(t, 100e-6, cost, 1e-12)
}

func TestComputeCacheCreationCostOpenAITTL(t *testing.T) {
	svc := newTestBillingService()
	for _, tc := range []struct {
		name         string
		short        int
		long         int
		aggregate    int
		longPrice    *float64
		wantCacheUSD float64
	}{
		{name: "30m only", short: 2399, long: 0, aggregate: 2399, longPrice: floatPtr(2e-6), wantCacheUSD: 2399e-6},
		{name: "12h only", short: 0, long: 2399, aggregate: 2399, longPrice: floatPtr(2e-6), wantCacheUSD: 2399 * 2e-6},
		{name: "mixed", short: 9, long: 2390, aggregate: 2399, longPrice: floatPtr(2e-6), wantCacheUSD: 9e-6 + 2390*2e-6},
		{name: "explicit free 12h", short: 9, long: 2390, aggregate: 2399, longPrice: floatPtr(0), wantCacheUSD: 9e-6},
		{name: "missing long price reuses short", short: 9, long: 2390, aggregate: 2399, longPrice: nil, wantCacheUSD: 2399e-6},
		// 桶不穷尽，回退聚合价。
		{name: "non-exhaustive falls back", short: 9, long: 2389, aggregate: 2399, longPrice: floatPtr(2e-6), wantCacheUSD: 2399e-6},
		// 没有 TTL 明细时不得推断 12h。
		{name: "no ttl buckets", short: 0, long: 0, aggregate: 2399, longPrice: floatPtr(2e-6), wantCacheUSD: 2399e-6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pricing := &ModelPricing{
				CacheCreation12hPricePerToken: tc.longPrice,
			}
			tokens := UsageTokens{
				CacheCreationTokens:    tc.aggregate,
				CacheCreation30mTokens: tc.short,
				CacheCreation12hTokens: tc.long,
			}
			cost := svc.computeCacheCreationCost(pricing, tokens, 1e-6, 1)
			require.InDelta(t, tc.wantCacheUSD, cost, 1e-12)
		})
	}
}

func TestComputeCacheCreationCostAppliesMultiplier(t *testing.T) {
	svc := newTestBillingService()
	pricing := &ModelPricing{CacheCreation12hPricePerToken: floatPtr(2e-6)}
	tokens := UsageTokens{CacheCreationTokens: 2399, CacheCreation30mTokens: 9, CacheCreation12hTokens: 2390}
	cost := svc.computeCacheCreationCost(pricing, tokens, 1e-6, 2)
	require.InDelta(t, (9e-6+2390*2e-6)*2, cost, 1e-12)
}

func TestGetModelPricingWithChannelCacheWriteTTL(t *testing.T) {
	svc := newTestBillingService()

	t.Run("1h enables breakdown and keeps 5m", func(t *testing.T) {
		pricing, err := svc.GetModelPricingWithChannel("gpt-6-luna", &ChannelModelPricing{
			CacheWritePrice:   floatPtr(1e-6),
			CacheWrite1hPrice: floatPtr(2e-6),
		})
		require.NoError(t, err)
		require.True(t, pricing.SupportsCacheBreakdown)
		require.InDelta(t, 1e-6, pricing.CacheCreation5mPrice, 1e-15)
		require.InDelta(t, 2e-6, pricing.CacheCreation1hPrice, 1e-15)
		require.Nil(t, pricing.CacheCreation12hPricePerToken)
	})

	t.Run("only flat price keeps legacy behavior", func(t *testing.T) {
		pricing, err := svc.GetModelPricingWithChannel("gpt-6-luna", &ChannelModelPricing{
			CacheWritePrice: floatPtr(1e-6),
		})
		require.NoError(t, err)
		require.False(t, pricing.SupportsCacheBreakdown)
		require.InDelta(t, 1e-6, pricing.CacheCreation1hPrice, 1e-15)
		require.Nil(t, pricing.CacheCreation12hPricePerToken)
	})

	t.Run("12h is exposed for OpenAI buckets", func(t *testing.T) {
		pricing, err := svc.GetModelPricingWithChannel("gpt-6-luna", &ChannelModelPricing{
			CacheWritePrice:    floatPtr(1e-6),
			CacheWrite12hPrice: floatPtr(3e-6),
		})
		require.NoError(t, err)
		require.NotNil(t, pricing.CacheCreation12hPricePerToken)
		require.InDelta(t, 3e-6, *pricing.CacheCreation12hPricePerToken, 1e-15)
	})
}

func TestOpenAICacheCreationTTLTokensFromUsage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		payload   string
		aggregate int
		short     int
		long      int
	}{
		{name: "responses api", payload: `{"input_tokens_details":{"cache_write_tokens":2399,"cache_write_tokens_details":{"ttl_30m_tokens":9,"ttl_12h_tokens":2390}}}`, aggregate: 2399, short: 9, long: 2390},
		{name: "chat completions", payload: `{"prompt_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":0,"ttl_12h_tokens":10}}}`, aggregate: 10, short: 0, long: 10},
		{name: "null details", payload: `{"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":null}}`, aggregate: 10},
		{name: "missing long bucket", payload: `{"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":10}}}`, aggregate: 10},
		{name: "non exhaustive", payload: `{"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":2,"ttl_12h_tokens":7}}}`, aggregate: 10},
		{name: "negative", payload: `{"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":-1,"ttl_12h_tokens":11}}}`, aggregate: 10},
		{name: "fractional", payload: `{"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":0.5,"ttl_12h_tokens":9.5}}}`, aggregate: 10},
		{name: "string values", payload: `{"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":"0","ttl_12h_tokens":"10"}}}`, aggregate: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			short, long := openAICacheCreationTTLTokensFromUsage(gjson.Parse(tc.payload), tc.aggregate)
			require.Equal(t, tc.short, short)
			require.Equal(t, tc.long, long)
		})
	}
}

func TestOpenAIUsageFromGJSONCacheWriteTTL(t *testing.T) {
	usage, ok := openAIUsageFromGJSON(gjson.Parse(`{
		"input_tokens": 2402,
		"input_tokens_details": {"cached_tokens": 0, "cache_write_tokens": 2399, "cache_write_tokens_details": {"ttl_30m_tokens": 9, "ttl_12h_tokens": 2390}},
		"output_tokens": 5,
		"total_tokens": 2407
	}`))
	require.True(t, ok)
	require.Equal(t, 2399, usage.CacheCreationInputTokens)
	require.Equal(t, 9, usage.CacheCreation30mInputTokens)
	require.Equal(t, 2390, usage.CacheCreation12hInputTokens)

	usage, ok = openAIUsageFromGJSON(gjson.Parse(`{
		"input_tokens": 10,
		"input_tokens_details": {"cache_write_tokens": 10, "cache_write_tokens_details": {"ttl_30m_tokens": 2, "ttl_12h_tokens": 7}},
		"output_tokens": 1
	}`))
	require.True(t, ok)
	require.Equal(t, 10, usage.CacheCreationInputTokens)
	require.Zero(t, usage.CacheCreation30mInputTokens)
	require.Zero(t, usage.CacheCreation12hInputTokens)
}

func TestDefaultOpenAICacheWrite12hPolicy(t *testing.T) {
	svc := newTestBillingService()
	pricing, err := svc.GetModelPricing("gpt-6-luna")
	require.NoError(t, err)
	require.NotNil(t, pricing.CacheCreation12hPricePerToken)
	require.InDelta(t, 0.25e-6, *pricing.CacheCreation12hPricePerToken, 1e-15)

	// 默认 12h 价参与实际计费：9 x 30m + 2390 x 12h。
	cost := svc.computeTokenBreakdown(pricing, UsageTokens{
		CacheCreationTokens:    2399,
		CacheCreation30mTokens: 9,
		CacheCreation12hTokens: 2390,
	}, 1, nil, "", false)
	require.InDelta(t, (9*0.125+2390*0.25)/1e6, cost.CacheCreationCost, 1e-12)
}

// 裸 gpt-6 在本地不属于 Astra 别名（见 openai_model_alias_test.go 的显式断言），
// 因此不套用 Astra 的 12h 默认价策略。
func TestDefaultOpenAICacheWrite12hPolicyBareGPT6Alias(t *testing.T) {
	svc := newTestBillingService()
	out := svc.applyModelSpecificPricingPolicyEx("gpt-6", &ModelPricing{
		InputPricePerToken:  0.1e-6,
		OutputPricePerToken: 0.5e-6,
	}, false, time.Now())
	require.Nil(t, out.CacheCreation12hPricePerToken)
}

func TestClaudeFableCacheWriteFallbackPricing(t *testing.T) {
	svc := newTestBillingService()
	for _, tc := range []struct {
		model     string
		cacheRead float64
	}{
		{model: "claude-fable-5", cacheRead: 1e-6},
		{model: "claude-fable-5-1", cacheRead: 0.25e-6},
	} {
		t.Run(tc.model, func(t *testing.T) {
			pricing, err := svc.GetModelPricing(tc.model)
			require.NoError(t, err)
			require.InDelta(t, 10e-6, pricing.InputPricePerToken, 1e-15)
			require.InDelta(t, 50e-6, pricing.OutputPricePerToken, 1e-15)
			require.InDelta(t, 12.5e-6, pricing.CacheCreation5mPrice, 1e-15)
			require.InDelta(t, 20e-6, pricing.CacheCreation1hPrice, 1e-15)
			require.InDelta(t, tc.cacheRead, pricing.CacheReadPricePerToken, 1e-15)
			require.True(t, pricing.SupportsCacheBreakdown)
		})
	}
}

func TestCalculateTokenStatsCostCacheWriteTTL(t *testing.T) {
	t.Run("anthropic 5m/1h", func(t *testing.T) {
		cost := calculateTokenStatsCost(&ChannelModelPricing{
			InputPrice:        floatPtr(1e-6),
			OutputPrice:       floatPtr(2e-6),
			CacheWritePrice:   floatPtr(3e-6),
			CacheWrite1hPrice: floatPtr(4e-6),
		}, UsageTokens{
			InputTokens:           10,
			OutputTokens:          5,
			CacheCreationTokens:   100,
			CacheCreation5mTokens: 40,
			CacheCreation1hTokens: 60,
		})
		require.NotNil(t, cost)
		require.InDelta(t, 10e-6+10e-6+40*3e-6+60*4e-6, *cost, 1e-12)
	})

	t.Run("openai 30m/12h", func(t *testing.T) {
		cost := calculateTokenStatsCost(&ChannelModelPricing{
			InputPrice:         floatPtr(1e-6),
			OutputPrice:        floatPtr(2e-6),
			CacheWritePrice:    floatPtr(3e-6),
			CacheWrite12hPrice: floatPtr(5e-6),
		}, UsageTokens{
			InputTokens:            10,
			OutputTokens:           5,
			CacheCreationTokens:    100,
			CacheCreation30mTokens: 40,
			CacheCreation12hTokens: 60,
		})
		require.NotNil(t, cost)
		require.InDelta(t, 10e-6+10e-6+40*3e-6+60*5e-6, *cost, 1e-12)
	})
}
