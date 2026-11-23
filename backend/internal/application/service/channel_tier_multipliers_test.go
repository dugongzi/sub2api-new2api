//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 渠道覆盖 Standard 价时应保留模型目录里的 Fast/Priority 比例，
// 而不是把所有档位统一压成 2x。
func TestGetModelPricingWithChannel_PreservesCatalogPriorityRatio(t *testing.T) {
	svc := newTestBillingService()

	// gpt-5.4 目录价：input 2.5/5（2x），output 15/30（2x）。
	pricing, err := svc.GetModelPricingWithChannel("gpt-5.4", &ChannelModelPricing{
		InputPrice:  testPtrFloat64(4e-6),
		OutputPrice: testPtrFloat64(30e-6),
	})
	require.NoError(t, err)

	require.InDelta(t, 4e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 8e-6, pricing.InputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 30e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 60e-6, pricing.OutputPricePerTokenPriority, 1e-12)
}

// 目录没有档位价时不得凭空推导 priority 价，应交给通用倍率兜底。
func TestGetModelPricingWithChannel_NoCatalogTierLeavesPriorityZero(t *testing.T) {
	svc := newTestBillingService()

	pricing, err := svc.GetModelPricingWithChannel("claude-sonnet-4", &ChannelModelPricing{
		InputPrice: testPtrFloat64(99e-6),
	})
	require.NoError(t, err)

	require.InDelta(t, 99e-6, pricing.InputPricePerToken, 1e-12)
	require.Zero(t, pricing.InputPricePerTokenPriority)
}

func TestGetModelPricingWithChannel_CopiesTierMultipliers(t *testing.T) {
	svc := newTestBillingService()

	pricing, err := svc.GetModelPricingWithChannel("gpt-5.4", &ChannelModelPricing{
		FastMultiplier: testPtrFloat64(3),
		FlexMultiplier: testPtrFloat64(0.4),
	})
	require.NoError(t, err)
	require.NotNil(t, pricing.FastMultiplier)
	require.InDelta(t, 3, *pricing.FastMultiplier, 1e-12)
	require.NotNil(t, pricing.FlexMultiplier)
	require.InDelta(t, 0.4, *pricing.FlexMultiplier, 1e-12)
}

func TestConfiguredServiceTierMultiplier_UsesChannelOverride(t *testing.T) {
	pricing := &ModelPricing{
		FastMultiplier: testPtrFloat64(3),
		FlexMultiplier: testPtrFloat64(0.4),
	}

	require.InDelta(t, 3, configuredServiceTierMultiplier("priority", pricing), 1e-12)
	require.InDelta(t, 3, configuredServiceTierMultiplier("fast", pricing), 1e-12)
	require.InDelta(t, 0.4, configuredServiceTierMultiplier("flex", pricing), 1e-12)
	// 未配置档位沿用通用倍率。
	require.InDelta(t, 1, configuredServiceTierMultiplier("", pricing), 1e-12)
}

func TestConfiguredServiceTierMultiplier_FallsBackToDefaults(t *testing.T) {
	pricing := &ModelPricing{}

	require.InDelta(t, 2, configuredServiceTierMultiplier("priority", pricing), 1e-12)
	require.InDelta(t, 0.5, configuredServiceTierMultiplier("flex", pricing), 1e-12)
	require.InDelta(t, 1, configuredServiceTierMultiplier("standard", pricing), 1e-12)
}

func TestConfiguredServiceTierMultiplier_PrefersUltrafastCard(t *testing.T) {
	pricing := &ModelPricing{UltrafastCostMultiplier: 6}

	require.InDelta(t, 6, configuredServiceTierMultiplier("ultrafast", pricing), 1e-12)
}

// 渠道配置 Fast 倍率后，priority 请求应改用该倍率而不是目录 priority 单价。
func TestComputeTokenBreakdown_ChannelFastMultiplierOverridesCatalogTier(t *testing.T) {
	svc := &BillingService{}
	pricing := &ModelPricing{
		InputPricePerToken:             4e-6,
		InputPricePerTokenPriority:     8e-6, // 目录比例 2x
		OutputPricePerToken:            30e-6,
		OutputPricePerTokenPriority:    60e-6,
		FastMultiplier:                 testPtrFloat64(3),
		CacheReadPricePerToken:         1e-6,
		CacheReadPricePerTokenPriority: 2e-6,
	}

	bd := svc.computeTokenBreakdown(pricing, UsageTokens{InputTokens: 1000}, 1, nil, "priority", false)

	// FastMultiplier 存在时不再走 priority 单价，而是标准价 × 3。
	require.InDelta(t, 1000*4e-6*3, bd.InputCost, 1e-12)
}

func TestIntervalToModelPricing_AppliesInputMultiplier(t *testing.T) {
	base := &ModelPricing{
		InputPricePerToken:                 2e-6,
		InputPricePerTokenPriority:         4e-6,
		OutputPricePerToken:                10e-6,
		OutputPricePerTokenPriority:        20e-6,
		CacheCreationPricePerToken:         2e-6,
		CacheCreationPricePerTokenPriority: 4e-6,
		CacheCreation5mPrice:               2e-6,
		CacheCreation1hPrice:               3e-6,
		CacheReadPricePerToken:             0.2e-6,
		CacheReadPricePerTokenPriority:     0.4e-6,
	}
	iv := &PricingInterval{
		InputMultiplier:      testPtrFloat64(1.5),
		OutputMultiplier:     testPtrFloat64(2),
		CacheWriteMultiplier: testPtrFloat64(1.25),
		CacheReadMultiplier:  testPtrFloat64(0.5),
	}

	pricing := intervalToModelPricing(iv, base, nil)

	require.InDelta(t, 3e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 6e-6, pricing.InputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 20e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 40e-6, pricing.OutputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 2.5e-6, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, 2.5e-6, pricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 3.75e-6, pricing.CacheCreation1hPrice, 1e-12)
	require.InDelta(t, 0.1e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.InDelta(t, 0.2e-6, pricing.CacheReadPricePerTokenPriority, 1e-12)
}

func TestIntervalToModelPricing_ExplicitPriceWinsOverMultiplier(t *testing.T) {
	base := &ModelPricing{InputPricePerToken: 2e-6, InputPricePerTokenPriority: 4e-6}
	iv := &PricingInterval{
		InputPrice:      testPtrFloat64(5e-6),
		InputMultiplier: testPtrFloat64(9),
	}

	pricing := intervalToModelPricing(iv, base, nil)

	require.InDelta(t, 5e-6, pricing.InputPricePerToken, 1e-12)
	// 目录比例 2x 保留。
	require.InDelta(t, 10e-6, pricing.InputPricePerTokenPriority, 1e-12)
}
