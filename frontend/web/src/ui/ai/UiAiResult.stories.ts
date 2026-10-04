import type { Meta, StoryObj } from '@storybook/vue3-vite'
import UiAiResult from './UiAiResult.vue'

const meta: Meta<typeof UiAiResult> = {
  title: 'Ui/Ai/UiAiResult',
  component: UiAiResult,
  argTypes: {
    state: {
      control: 'select',
      options: [
        'idle',
        'generating',
        'done',
        'stopped',
        'error',
        'unavailable',
      ],
    },
    showStop: {
      control: 'boolean',
    },
  },
}

export default meta
type Story = StoryObj<typeof UiAiResult>

export const FullSummary: Story = {
  args: {
    state: 'done',
    result: {
      type: 'summary',
      tone: 'warning',
      headline: 'Berlin Mitte distribution layer is degraded',
      findings: [
        {
          title: 'Port ge-0/0/1 flapping on dist-sw-1',
          severity: 'warning',
          refs: [{ kind: 'device', id: 'dist-sw-1', label: 'dist-sw-1' }],
        },
        {
          title: 'Secondary power feed lost',
          severity: 'critical',
          refs: [],
        },
      ],
      cause: {
        text: 'Optical transceiver failure on port ge-0/0/1',
        confidence: 'medium',
        refs: [{ kind: 'device', id: 'core-sw-1', label: 'core-sw-1' }],
      },
      impact: {
        text: 'Backup uplink took over traffic with 30ms latency increase',
        refs: [],
      },
      metrics: [
        { label: 'DropRate', value: '12.4%' },
        { label: 'LinkFlaps', value: '42' },
        { label: 'PSU_Status', value: 'Degraded' },
      ],
      next: [
        { label: 'Replace SFP module on ge-0/0/1' },
        { label: 'Verify CRC counter on core-sw-1' },
      ],
      sources: [
        { kind: 'site', id: 'site-berlin', label: 'Berlin Mitte' },
        { kind: 'device', id: 'dist-sw-1', label: 'dist-sw-1' },
      ],
    },
  },
}

export const HealthySummary: Story = {
  args: {
    state: 'done',
    result: {
      type: 'summary',
      tone: 'ok',
      headline: 'All devices in Berlin Mitte are operational.',
      findings: [
        {
          title: 'Zero port flaps in the last 24 hours.',
          severity: 'info',
          refs: [],
        },
      ],
      metrics: [
        { label: 'Uptime', value: '99.99%' },
        { label: 'Throughput', value: '2.4 Gbps' },
      ],
      next: [],
      sources: [],
    },
  },
}

export const GeneratingSkeleton: Story = {
  args: {
    state: 'generating',
    result: null,
    showStop: true,
  },
}

export const StoppedState: Story = {
  args: {
    state: 'stopped',
    result: {
      type: 'summary',
      headline: 'Partial generation before the operator stopped the run.',
      tone: 'unknown',
      findings: [],
      metrics: [],
      next: [],
      sources: [],
    },
  },
}

export const ErrorState: Story = {
  args: {
    state: 'error',
    error: 'The AI returned a result FlowSeer cannot show.',
  },
}

export const UnavailableState: Story = {
  args: {
    state: 'unavailable',
  },
}
