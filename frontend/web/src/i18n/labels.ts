import { useI18n } from 'vue-i18n'
import type { Band } from '../domain/clients'
import type { Health, Lifecycle, Reachability } from '../domain/fleet'
import type { Severity } from '../domain/overview'
import type { PortStatus } from '../domain/telemetry'
import type { PageView } from '../navigation/page'

type Medium = 'Fiber' | 'Copper'
type Mode = 'Trunk' | 'Access' | 'Routed'
type Duplex = 'Full' | 'Half'
type Signal = 'Strong' | 'Fair' | 'Weak'

const PAGES = {
  dashboard: 'view.common.pages.dashboard',
  devices: 'view.common.pages.devices',
  clients: 'view.common.pages.clients',
  sites: 'view.common.pages.sites',
  topology: 'view.common.pages.topology',
  device: 'view.common.pages.device',
} as const satisfies Record<PageView, string>

const HEALTH = {
  Healthy: 'view.common.health.healthy',
  Degraded: 'view.common.health.degraded',
  Offline: 'view.common.health.offline',
} as const satisfies Record<Health, string>

const SEVERITY = {
  critical: 'view.common.severity.critical',
  warning: 'view.common.severity.warning',
  info: 'view.common.severity.info',
} as const satisfies Record<Severity, string>

const REACHABILITY = {
  Reachable: 'view.common.reachability.reachable',
  Unreachable: 'view.common.reachability.unreachable',
} as const satisfies Record<Reachability, string>

const LIFECYCLE = {
  Active: 'view.common.lifecycle.active',
  Retired: 'view.common.lifecycle.retired',
} as const satisfies Record<Lifecycle, string>

const PORT_STATUS = {
  Up: 'view.common.portStatus.up',
  Down: 'view.common.portStatus.down',
  Disabled: 'view.common.portStatus.disabled',
} as const satisfies Record<PortStatus, string>

const BAND = {
  '2.4 GHz': 'view.common.band.ghz2',
  '5 GHz': 'view.common.band.ghz5',
  '6 GHz': 'view.common.band.ghz6',
} as const satisfies Record<Band, string>

const MEDIUM = {
  Fiber: 'view.common.medium.fiber',
  Copper: 'view.common.medium.copper',
} as const satisfies Record<Medium, string>

const MODE = {
  Trunk: 'view.common.mode.trunk',
  Access: 'view.common.mode.access',
  Routed: 'view.common.mode.routed',
} as const satisfies Record<Mode, string>

const DUPLEX = {
  Full: 'view.common.duplex.full',
  Half: 'view.common.duplex.half',
} as const satisfies Record<Duplex, string>

const SIGNAL = {
  Strong: 'view.common.signal.strong',
  Fair: 'view.common.signal.fair',
  Weak: 'view.common.signal.weak',
} as const satisfies Record<Signal, string>

// Names identifiers and page ids for the active locale. Each call reads the
// global Composer, so a label follows a locale switch.
export function useLabels() {
  const { t, n } = useI18n({ useScope: 'global' })

  // A rollup's health line: the offline and degraded counts, or all healthy.
  function healthLine(health: Record<Health, number>): string {
    const parts = [
      health.Offline &&
        t('view.common.healthLine.offline', {
          count: n(health.Offline, 'integer'),
        }),
      health.Degraded &&
        t('view.common.healthLine.degraded', {
          count: n(health.Degraded, 'integer'),
        }),
    ].filter(Boolean)
    return parts.length
      ? parts.join(t('view.common.factSeparator'))
      : t('view.common.healthLine.allHealthy')
  }

  return {
    page: (id: PageView) => t(PAGES[id]),
    health: (value: Health) => t(HEALTH[value]),
    severity: (value: Severity) => t(SEVERITY[value]),
    reachability: (value: Reachability) => t(REACHABILITY[value]),
    lifecycle: (value: Lifecycle) => t(LIFECYCLE[value]),
    portStatus: (value: PortStatus) => t(PORT_STATUS[value]),
    band: (value: Band) => t(BAND[value]),
    medium: (value: Medium) => t(MEDIUM[value]),
    mode: (value: Mode) => t(MODE[value]),
    duplex: (value: Duplex) => t(DUPLEX[value]),
    signal: (value: Signal) => t(SIGNAL[value]),
    healthLine,
  }
}
