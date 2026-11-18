import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import TimePricingSection from '@/features/admin-channels/presentation/widgets/TimePricingSection.vue'

vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

const SelectStub = {
  name: 'Select',
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: '<button type="button" v-bind="$attrs" />',
}

describe('TimePricingSection day scope', () => {
  it('offers every-day and weekdays-only scopes and emits weekdays_only', async () => {
    const value = { timezone: 'Asia/Shanghai', periods: [] }
    const wrapper = mount(TimePricingSection, {
      props: { modelValue: value },
      global: { stubs: { Select: SelectStub, Icon: true } },
    })

    const scope = wrapper.getComponent(SelectStub)
    expect(scope.props('modelValue')).toBe(false)
    expect(scope.props('options')).toEqual([
      { value: false, label: 'admin.channels.form.timePricingEveryDay' },
      { value: true, label: 'admin.channels.form.timePricingWeekdaysOnly' },
    ])

    scope.vm.$emit('update:modelValue', true)
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toEqual({
      ...value,
      weekdays_only: true,
    })
  })

  it('reflects weekdays_only from the bound value', () => {
    const wrapper = mount(TimePricingSection, {
      props: { modelValue: { timezone: 'Asia/Shanghai', weekdays_only: true, periods: [] } },
      global: { stubs: { Select: SelectStub, Icon: true } },
    })

    expect(wrapper.getComponent(SelectStub).props('modelValue')).toBe(true)
  })
})
