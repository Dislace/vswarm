import type { BrowserContext } from "playwright-core"

// A dev server in this workspace answers the operator's browser at
// https://<port>-<label>.<zone>, through Cloudflare Access and the vswarm proxy.
// This browser runs inside the workspace, with no Access session and no route
// to the proxy, so here the same hostname is served from the dev server
// directly: an agent opens one URL whichever browser the preview turns out to be.
export const routeWorkspaceHostnames = async (
  context: BrowserContext,
  domain: string | undefined,
): Promise<void> => {
  const dot = domain?.indexOf(".") ?? -1
  if (domain === undefined || dot <= 0) return
  const escape = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
  const hostname = new RegExp(
    `^(\\d+)-${escape(domain.slice(0, dot))}\\.${escape(domain.slice(dot + 1))}$`,
  )
  const portOf = (url: URL) => hostname.exec(url.hostname)?.[1]
  const local = (url: URL, scheme: string) =>
    `${scheme}://127.0.0.1:${portOf(url)}${url.pathname}${url.search}`

  await context.route(
    (url) => portOf(url) !== undefined,
    async (route) => {
      const response = await route.fetch({ url: local(new URL(route.request().url()), "http") })
      await route.fulfill({ response })
    },
  )

  await context.routeWebSocket(
    (url) => portOf(url) !== undefined,
    (page) => {
      const server = new WebSocket(local(new URL(page.url()), "ws"), page.protocols())
      server.binaryType = "arraybuffer"
      const early: Array<string | Buffer> = []
      page.onMessage((message) => {
        if (server.readyState === WebSocket.OPEN) server.send(message)
        else early.push(message)
      })
      server.onopen = () => {
        for (const message of early.splice(0)) server.send(message)
      }
      server.onmessage = (event) =>
        page.send(typeof event.data === "string" ? event.data : Buffer.from(event.data))
      // A client may only close with 1000 or 3000-4999, and 1005/1006 are never
      // sent on the wire, so codes are not forwarded blindly in either direction.
      page.onClose(() => server.close())
      server.onclose = (event) =>
        page.close({
          code: event.code === 1005 || event.code === 1006 ? 1000 : event.code,
          reason: event.reason,
        })
    },
  )
}
