import ELK from 'elkjs/lib/elk.bundled.js'
import type { ElkNode } from 'elkjs/lib/elk-api'
import type { Edge, Node } from '@vue-flow/core'
import type { Device, Link, Site } from '../../domain/fleet'

// Device cards render at this size; ELK needs it up front to place them.
export const NODE_WIDTH = 212
export const NODE_HEIGHT = 86
const SITE_HEADER = 64
const SITE_INSET = 28

const elk = new ELK()

// Each site is laid out top-down from its gateway, then the sites are packed
// side by side. Positions of devices are relative to their site, which is
// what Vue Flow expects for child nodes.
export async function layoutTopology(
  sites: Site[],
  devices: Device[],
  links: Link[],
  aspectRatio: number,
): Promise<{ nodes: Node[]; edges: Edge[] }> {
  const graph: ElkNode = {
    id: 'root',
    layoutOptions: {
      'elk.algorithm': 'rectpacking',
      'elk.spacing.nodeNode': '32',
      'elk.aspectRatio': String(aspectRatio),
    },
    children: sites.map((site) => ({
      id: `site-${site.id}`,
      layoutOptions: {
        'elk.algorithm': 'layered',
        'elk.direction': 'DOWN',
        // A lone site draws no header; the breadcrumb already names it.
        'elk.padding': `[top=${sites.length > 1 ? SITE_HEADER : SITE_INSET},left=${SITE_INSET},bottom=${SITE_INSET},right=${SITE_INSET}]`,
        'elk.spacing.nodeNode': '24',
        'elk.layered.spacing.nodeNodeBetweenLayers': '84',
      },
      children: devices
        .filter((device) => device.siteId === site.id)
        .map((device) => ({
          id: device.id,
          width: NODE_WIDTH,
          height: NODE_HEIGHT,
        })),
      edges: links
        .filter((link) =>
          devices.some((d) => d.id === link.targetId && d.siteId === site.id),
        )
        .map((link) => ({
          id: link.id,
          sources: [link.sourceId],
          targets: [link.targetId],
        })),
    })),
  }
  const laid = await elk.layout(graph)
  const nodes: Node[] = []
  for (const site of laid.children ?? []) {
    nodes.push({
      id: site.id,
      type: 'site',
      position: { x: site.x ?? 0, y: site.y ?? 0 },
      data: { siteId: site.id.replace(/^site-/, '') },
      style: { width: `${site.width ?? 0}px`, height: `${site.height ?? 0}px` },
      selectable: false,
      focusable: false,
    })
    for (const device of site.children ?? [])
      nodes.push({
        id: device.id,
        type: 'device',
        parentNode: site.id,
        extent: 'parent',
        position: { x: device.x ?? 0, y: device.y ?? 0 },
        data: { deviceId: device.id },
      })
  }
  const edges: Edge[] = links.map((link) => ({
    id: link.id,
    type: 'link',
    source: link.sourceId,
    target: link.targetId,
    data: { linkId: link.id },
  }))
  return { nodes, edges }
}
