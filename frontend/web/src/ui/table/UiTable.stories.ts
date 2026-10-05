import type { Meta, StoryObj } from '@storybook/vue3-vite'
import { ref } from 'vue'
import type { AiOriginRequest } from '../ai/context'
import UiAiLabel from '../ai/UiAiLabel.vue'
import UiTable from './UiTable.vue'
import UiTableHeader from './UiTableHeader.vue'
import UiTableBody from './UiTableBody.vue'
import UiTableRow from './UiTableRow.vue'
import UiTableHead from './UiTableHead.vue'
import UiTableCell from './UiTableCell.vue'
import UiTableEmpty from './UiTableEmpty.vue'
import UiScrollArea from '../scroll-area/UiScrollArea.vue'

const meta: Meta<typeof UiTable> = {
  title: 'Ui/Table',
  component: UiTable,
  argTypes: {
    dense: { control: 'boolean' },
    stickyHeader: { control: 'boolean' },
  },
  // A story too wide for a narrow viewport opts into its own horizontal
  // scroll region, as the app's call sites do, so the page never scrolls.
  // UiTable adds no scroller itself: a sticky header needs the caller's
  // vertical scroller as its nearest scroll container.
  decorators: [
    (story, context) => ({
      components: { story, UiScrollArea },
      template:
        context.parameters.tableScroller === true
          ? '<UiScrollArea axis="x" label="Devices"><story /></UiScrollArea>'
          : '<story />',
    }),
  ],
}

export default meta
type Story = StoryObj<typeof UiTable>

const sampleDevices = [
  {
    id: 'dev-1',
    name: 'edge-router-01',
    ip: '10.0.0.1',
    status: 'Healthy',
    throughput: '840 Mbps',
  },
  {
    id: 'dev-2',
    name: 'core-switch-02',
    ip: '10.0.0.2',
    status: 'Healthy',
    throughput: '1.2 Gbps',
  },
  {
    id: 'dev-3',
    name: 'access-point-03',
    ip: '10.0.0.15',
    status: 'Degraded',
    throughput: '120 Mbps',
  },
  {
    id: 'dev-4',
    name: 'firewall-gw-04',
    ip: '10.0.0.254',
    status: 'Offline',
    throughput: '0 Mbps',
  },
]

export const Default: Story = {
  parameters: { tableScroller: true },
  render: (args) => ({
    components: {
      UiTable,
      UiTableHeader,
      UiTableBody,
      UiTableRow,
      UiTableHead,
      UiTableCell,
    },
    setup() {
      const aiTarget = (dev: (typeof sampleDevices)[number]) => ({
        id: `standalone:story:ui-table-default:${dev.id}`,
        kind: 'device',
        label: dev.name,
        context: { ip: dev.ip, status: dev.status },
      })
      return { args, devices: sampleDevices, aiTarget }
    },
    template: `
      <UiTable v-bind="args">
        <UiTableHeader>
          <UiTableRow>
            <UiTableHead>Name</UiTableHead>
            <UiTableHead>IP Address</UiTableHead>
            <UiTableHead>Status</UiTableHead>
            <UiTableHead align="numeric">Throughput</UiTableHead>
          </UiTableRow>
        </UiTableHeader>
        <UiTableBody>
          <UiTableRow
            v-for="dev in devices"
            :key="dev.id"
            :ai="aiTarget(dev)"
          >
            <UiTableCell>{{ dev.name }}</UiTableCell>
            <UiTableCell mono>{{ dev.ip }}</UiTableCell>
            <UiTableCell>{{ dev.status }}</UiTableCell>
            <UiTableCell align="numeric">{{ dev.throughput }}</UiTableCell>
          </UiTableRow>
        </UiTableBody>
      </UiTable>
    `,
  }),
}

export const Dense: Story = {
  parameters: { tableScroller: true },
  args: {
    dense: true,
  },
  render: (args) => ({
    components: {
      UiTable,
      UiTableHeader,
      UiTableBody,
      UiTableRow,
      UiTableHead,
      UiTableCell,
    },
    setup() {
      return { args, devices: sampleDevices }
    },
    template: `
      <UiTable v-bind="args">
        <UiTableHeader>
          <UiTableRow>
            <UiTableHead>Name</UiTableHead>
            <UiTableHead>IP Address</UiTableHead>
            <UiTableHead>Status</UiTableHead>
            <UiTableHead align="numeric">Throughput</UiTableHead>
          </UiTableRow>
        </UiTableHeader>
        <UiTableBody>
          <UiTableRow v-for="dev in devices" :key="dev.id">
            <UiTableCell>{{ dev.name }}</UiTableCell>
            <UiTableCell mono>{{ dev.ip }}</UiTableCell>
            <UiTableCell>{{ dev.status }}</UiTableCell>
            <UiTableCell align="numeric">{{ dev.throughput }}</UiTableCell>
          </UiTableRow>
        </UiTableBody>
      </UiTable>
    `,
  }),
}

export const Sortable: Story = {
  parameters: { tableScroller: true },
  render: () => ({
    components: {
      UiTable,
      UiTableHeader,
      UiTableBody,
      UiTableRow,
      UiTableHead,
      UiTableCell,
    },
    setup() {
      const sortDirection = ref<'ascending' | 'descending' | 'none'>('none')
      const devices = ref([...sampleDevices])

      function toggleSort() {
        if (sortDirection.value === 'ascending') {
          sortDirection.value = 'descending'
          devices.value.sort((a, b) => b.name.localeCompare(a.name))
        } else {
          sortDirection.value = 'ascending'
          devices.value.sort((a, b) => a.name.localeCompare(b.name))
        }
      }

      return { devices, sortDirection, toggleSort }
    },
    template: `
      <UiTable>
        <UiTableHeader>
          <UiTableRow>
            <UiTableHead
              sortable
              :sort-direction="sortDirection"
              @sort="toggleSort"
            >
              Device Name
            </UiTableHead>
            <UiTableHead>IP Address</UiTableHead>
            <UiTableHead>Status</UiTableHead>
            <UiTableHead align="numeric">Throughput</UiTableHead>
          </UiTableRow>
        </UiTableHeader>
        <UiTableBody>
          <UiTableRow v-for="dev in devices" :key="dev.id">
            <UiTableCell>{{ dev.name }}</UiTableCell>
            <UiTableCell mono>{{ dev.ip }}</UiTableCell>
            <UiTableCell>{{ dev.status }}</UiTableCell>
            <UiTableCell align="numeric">{{ dev.throughput }}</UiTableCell>
          </UiTableRow>
        </UiTableBody>
      </UiTable>
    `,
  }),
}

export const StickyHeader: Story = {
  args: {
    stickyHeader: true,
  },
  render: (args) => ({
    components: {
      UiTable,
      UiTableHeader,
      UiTableBody,
      UiTableRow,
      UiTableHead,
      UiTableCell,
    },
    setup() {
      const manyDevices = Array.from({ length: 20 }, (_, i) => ({
        id: `dev-${i + 1}`,
        name: `switch-${String(i + 1).padStart(2, '0')}`,
        ip: `10.0.1.${i + 1}`,
        status: i % 4 === 0 ? 'Degraded' : 'Healthy',
        throughput: `${(i + 1) * 50} Mbps`,
      }))
      return { args, devices: manyDevices }
    },
    template: `
      <div class="h-48 overflow-y-auto border border-border rounded-control">
        <UiTable v-bind="args">
          <UiTableHeader>
            <UiTableRow>
              <UiTableHead>Name</UiTableHead>
              <UiTableHead>IP Address</UiTableHead>
              <UiTableHead>Status</UiTableHead>
              <UiTableHead align="numeric">Throughput</UiTableHead>
            </UiTableRow>
          </UiTableHeader>
          <UiTableBody>
            <UiTableRow v-for="dev in devices" :key="dev.id">
              <UiTableCell>{{ dev.name }}</UiTableCell>
              <UiTableCell mono>{{ dev.ip }}</UiTableCell>
              <UiTableCell>{{ dev.status }}</UiTableCell>
              <UiTableCell align="numeric">{{ dev.throughput }}</UiTableCell>
            </UiTableRow>
          </UiTableBody>
        </UiTable>
      </div>
    `,
  }),
}

export const RowSelection: Story = {
  parameters: { tableScroller: true },
  render: () => ({
    components: {
      UiTable,
      UiTableHeader,
      UiTableBody,
      UiTableRow,
      UiTableHead,
      UiTableCell,
    },
    setup() {
      const selected = ref<Record<string, boolean>>({ 'dev-2': true })

      function toggleRow(id: string) {
        selected.value[id] = !selected.value[id]
      }

      return { devices: sampleDevices, selected, toggleRow }
    },
    template: `
      <UiTable>
        <UiTableHeader>
          <UiTableRow>
            <UiTableHead class="w-10">
              <span class="sr-only">Select</span>
            </UiTableHead>
            <UiTableHead>Name</UiTableHead>
            <UiTableHead>IP Address</UiTableHead>
            <UiTableHead>Status</UiTableHead>
            <UiTableHead align="numeric">Throughput</UiTableHead>
          </UiTableRow>
        </UiTableHeader>
        <UiTableBody>
          <UiTableRow
            v-for="dev in devices"
            :key="dev.id"
            :selected="!!selected[dev.id]"
          >
            <UiTableCell class="w-10">
              <input
                type="checkbox"
                :checked="!!selected[dev.id]"
                :aria-label="'Select ' + dev.name"
                class="rounded border-border accent-primary cursor-pointer"
                @change="toggleRow(dev.id)"
              />
            </UiTableCell>
            <UiTableCell>{{ dev.name }}</UiTableCell>
            <UiTableCell mono>{{ dev.ip }}</UiTableCell>
            <UiTableCell>{{ dev.status }}</UiTableCell>
            <UiTableCell align="numeric">{{ dev.throughput }}</UiTableCell>
          </UiTableRow>
        </UiTableBody>
      </UiTable>
    `,
  }),
}

export const Empty: Story = {
  render: () => ({
    components: {
      UiTable,
      UiTableHeader,
      UiTableBody,
      UiTableRow,
      UiTableHead,
      UiTableCell,
      UiTableEmpty,
    },
    template: `
      <UiTable>
        <UiTableHeader>
          <UiTableRow>
            <UiTableHead>Name</UiTableHead>
            <UiTableHead>IP Address</UiTableHead>
            <UiTableHead>Status</UiTableHead>
            <UiTableHead align="numeric">Throughput</UiTableHead>
          </UiTableRow>
        </UiTableHeader>
        <UiTableBody>
          <UiTableEmpty :col-span="4">
            No devices found matching filter criteria.
          </UiTableEmpty>
        </UiTableBody>
      </UiTable>
    `,
  }),
}

export const LongText: Story = {
  parameters: { tableScroller: true },
  render: () => ({
    components: {
      UiTable,
      UiTableHeader,
      UiTableBody,
      UiTableRow,
      UiTableHead,
      UiTableCell,
    },
    setup() {
      const items = [
        {
          id: 'dev-1',
          name: 'edge-router-distributed-datacenter-zone-north-01',
          ip: '10.250.128.1',
          status: 'Healthy and responding to bidirectional telemetry probes',
          throughput: '1240.5 megabits per second sustained',
        },
      ]
      return { items }
    },
    template: `
      <UiTable>
        <UiTableHeader>
          <UiTableRow>
            <UiTableHead>Fully Qualified Device Name</UiTableHead>
            <UiTableHead>Network Protocol Address</UiTableHead>
            <UiTableHead>Operational Telemetry Status</UiTableHead>
            <UiTableHead align="numeric">Cumulative Throughput Rate</UiTableHead>
          </UiTableRow>
        </UiTableHeader>
        <UiTableBody>
          <UiTableRow v-for="item in items" :key="item.id">
            <UiTableCell>{{ item.name }}</UiTableCell>
            <UiTableCell mono>{{ item.ip }}</UiTableCell>
            <UiTableCell>{{ item.status }}</UiTableCell>
            <UiTableCell align="numeric">{{ item.throughput }}</UiTableCell>
          </UiTableRow>
        </UiTableBody>
      </UiTable>
    `,
  }),
}

export const CustomSortMarks: Story = {
  render: () => ({
    components: {
      UiTable,
      UiTableHeader,
      UiTableBody,
      UiTableRow,
      UiTableHead,
      UiTableCell,
    },
    template: `
      <UiTable>
        <UiTableHeader>
          <UiTableRow>
            <UiTableHead sortable sort-direction="ascending" ascending-mark=" [ASC]">
              Device Name
            </UiTableHead>
            <UiTableHead sortable sort-direction="descending" descending-mark=" [DESC]">
              Status
            </UiTableHead>
          </UiTableRow>
        </UiTableHeader>
        <UiTableBody>
          <UiTableRow>
            <UiTableCell>edge-router-01</UiTableCell>
            <UiTableCell>Healthy</UiTableCell>
          </UiTableRow>
        </UiTableBody>
      </UiTable>
    `,
  }),
}

// A cell an agent changed carries the outline until the user touches it. Its
// explanation sits in the next cell, so the label never becomes a child of the
// table or row, and the caller clears both through the typed event.
export const AgentChanged: Story = {
  parameters: { tableScroller: true },
  render: () => ({
    components: {
      UiTable,
      UiTableHeader,
      UiTableBody,
      UiTableRow,
      UiTableHead,
      UiTableCell,
      UiAiLabel,
    },
    setup() {
      const target = {
        id: 'standalone:story:ui-table-agent-changed:status',
        kind: 'device',
        label: 'access-point-03',
        context: { status: 'Degraded' },
      }
      const origin = ref<AiOriginRequest | undefined>({
        requestId: 'req-story',
        action: 'summary',
        targets: [],
        history: [],
      })
      return { target, origin }
    },
    template: `
      <UiTable>
        <UiTableHeader>
          <UiTableRow>
            <UiTableHead>Name</UiTableHead>
            <UiTableHead>Status</UiTableHead>
            <UiTableHead>Explanation</UiTableHead>
          </UiTableRow>
        </UiTableHeader>
        <UiTableBody>
          <UiTableRow>
            <UiTableCell>access-point-03</UiTableCell>
            <UiTableCell
              :ai="target"
              :ai-origin="origin"
              @ai-origin-acknowledged="origin = undefined"
            >
              Degraded
            </UiTableCell>
            <UiTableCell>
              <UiAiLabel v-if="origin" :request="origin" />
            </UiTableCell>
          </UiTableRow>
        </UiTableBody>
      </UiTable>
    `,
  }),
}
