import { createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { Agentation, type AgentationProps } from 'agentation'

// Agentation is a React component, so it runs as its own React root beside
// the Vue app. The endpoint is the loopback HTTP server that the agentation
// MCP server (registered in the repository's .mcp.json) starts, which lets an
// agent read annotations directly instead of pasting them.
export function mountAgentation(): void {
  const host = document.createElement('div')
  host.id = 'agentation'
  document.body.append(host)
  createRoot(host).render(
    createElement<AgentationProps>(Agentation, {
      endpoint: 'http://localhost:4747',
    }),
  )
}
