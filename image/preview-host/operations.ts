import { devices } from "playwright-core"
import type { Locator, Page } from "playwright-core"
import type { Tab, TabRegistry, ViewportSetting } from "./tabs.ts"

const PRESET_DEVICES: Record<string, string> = {
  "iphone-se": "iPhone SE",
  "iphone-xr": "iPhone XR",
  "iphone-12-pro": "iPhone 12 Pro",
  "iphone-14-pro-max": "iPhone 14 Pro Max",
  "pixel-7": "Pixel 7",
  "samsung-galaxy-s8-plus": "Galaxy S8+",
  "samsung-galaxy-s20-ultra": "Galaxy S20 Ultra",
  "ipad-mini": "iPad Mini",
  "ipad-air": "iPad (gen 7)",
  "ipad-pro": "iPad Pro 11",
  "surface-pro-7": "Desktop Chrome",
  "surface-duo": "Galaxy Tab S4",
  "galaxy-z-fold-5": "Galaxy S9+",
  "asus-zenbook-fold": "Desktop Chrome",
  "samsung-galaxy-a51-71": "Galaxy S8",
  "nest-hub": "Desktop Chrome",
  "nest-hub-max": "Desktop Chrome",
}

const FALLBACK_VIEWPORT = { width: 1280, height: 720 }

type Input = Record<string, unknown>

const str = (input: Input, key: string): string | undefined => {
  const value = input[key]
  return typeof value === "string" ? value : undefined
}

const num = (input: Input, key: string): number | undefined => {
  const value = input[key]
  return typeof value === "number" ? value : undefined
}

const bool = (input: Input, key: string): boolean | undefined => {
  const value = input[key]
  return typeof value === "boolean" ? value : undefined
}

const target = (page: Page, input: Input): Locator | null => {
  const locator = str(input, "locator")
  if (locator !== undefined) return page.locator(locator)
  const selector = str(input, "selector")
  if (selector !== undefined) return page.locator(selector)
  return null
}

const viewportOf = (page: Page) => page.viewportSize() ?? FALLBACK_VIEWPORT

const status = (tab: Tab) => ({
  available: true,
  visible: true,
  tabId: tab.id,
  url: tab.page.url(),
  title: null as string | null,
  loading: false,
  viewportSetting: tab.viewport,
  viewport: viewportOf(tab.page),
})

const statusWithTitle = async (tab: Tab) => ({
  ...status(tab),
  title: await tab.page.title().catch(() => null),
})

const INTERACTIVE = "a, button, input, select, textarea, summary, [role], [tabindex], [onclick]"

const collectElements = (page: Page) =>
  page.evaluate((selector: string) => {
    const seen = document.querySelectorAll(selector)
    const out: Array<Record<string, unknown>> = []
    for (const node of Array.from(seen).slice(0, 200)) {
      const element = node as HTMLElement
      const box = element.getBoundingClientRect()
      if (box.width === 0 && box.height === 0) continue
      const id = element.getAttribute("id")
      out.push({
        tag: element.tagName.toLowerCase(),
        role: element.getAttribute("role"),
        name:
          element.getAttribute("aria-label") ??
          (element.textContent ?? "").trim().slice(0, 120),
        selector: id !== null && id !== "" ? `#${id}` : element.tagName.toLowerCase(),
        x: box.x,
        y: box.y,
        width: box.width,
        height: box.height,
      })
    }
    return out
  }, INTERACTIVE)

export const dispatch = async (
  tabs: TabRegistry,
  operation: string,
  rawInput: unknown,
  timeoutMs: number,
): Promise<unknown> => {
  const input: Input = (rawInput ?? {}) as Input
  const timeout = num(input, "timeoutMs") ?? timeoutMs

  if (operation === "open") {
    const requested = str(input, "tabId")
    const reuse = bool(input, "reuseExistingTab") ?? true
    const tab =
      requested !== undefined || reuse ? await tabs.resolve(requested) : await tabs.open()
    const url = str(input, "url")
    if (url !== undefined) {
      await tab.page.goto(url, { timeout, waitUntil: "load" })
    }
    return await statusWithTitle(tab)
  }

  const tab = await tabs.resolve(str(input, "tabId"))
  tabs.focus(tab.id)
  const page = tab.page

  switch (operation) {
    case "status":
      return await statusWithTitle(tab)

    case "navigate":
      return await tabs.record(tab, "navigate", async () => {
        const url = str(input, "url")
        const readiness = str(input, "readiness") ?? "load"
        const resolved = url ?? environmentPort(input)
        if (resolved === undefined) throw new Error("navigate requires url or target")
        await page.goto(resolved, {
          timeout,
          waitUntil:
            readiness === "none"
              ? "commit"
              : readiness === "domContentLoaded"
                ? "domcontentloaded"
                : "load",
        })
        return await statusWithTitle(tab)
      })

    case "click":
      return await tabs.record(tab, "click", async () => {
        const x = num(input, "x")
        const y = num(input, "y")
        if (x !== undefined && y !== undefined) {
          await page.mouse.click(x, y)
          return {}
        }
        const locator = target(page, input)
        if (locator === null) throw new Error("click requires locator, selector, or x and y")
        await locator.click({ timeout })
        return {}
      })

    case "type":
      return await tabs.record(tab, "type", async () => {
        const text = str(input, "text") ?? ""
        const locator = target(page, input)
        if (locator === null) {
          await page.keyboard.insertText(text)
          return {}
        }
        if (bool(input, "clear") === true) await locator.fill("", { timeout })
        await locator.pressSequentially(text, { timeout })
        return {}
      })

    case "press":
      return await tabs.record(tab, "press", async () => {
        const key = str(input, "key")
        if (key === undefined) throw new Error("press requires key")
        const modifiers = Array.isArray(input["modifiers"])
          ? (input["modifiers"] as string[])
          : []
        await page.keyboard.press([...modifiers, key].join("+"))
        return {}
      })

    case "scroll":
      return await tabs.record(tab, "scroll", async () => {
        const deltaX = num(input, "deltaX") ?? 0
        const deltaY = num(input, "deltaY") ?? 0
        const locator = target(page, input)
        if (locator === null) {
          await page.evaluate(
            (delta: { x: number; y: number }) => {
              window.scrollBy(delta.x, delta.y)
            },
            { x: deltaX, y: deltaY },
          )
          return {}
        }
        await locator.evaluate(
          (node: Element, delta: { x: number; y: number }) => {
            node.scrollBy(delta.x, delta.y)
          },
          { x: deltaX, y: deltaY },
        )
        return {}
      })

    case "evaluate":
      return await tabs.record(tab, "evaluate", async () => {
        const expression = str(input, "expression")
        if (expression === undefined) throw new Error("evaluate requires expression")
        const value = await page.evaluate<unknown, string>(
          (source) => (0, eval)(source),
          expression,
        )
        return { value }
      })

    case "waitFor":
      return await tabs.record(tab, "waitFor", async () => {
        const locator = target(page, input)
        if (locator !== null) await locator.waitFor({ timeout, state: "visible" })
        const text = str(input, "text")
        if (text !== undefined) {
          await page.getByText(text).first().waitFor({ timeout, state: "visible" })
        }
        const urlIncludes = str(input, "urlIncludes")
        if (urlIncludes !== undefined) {
          await page.waitForURL((url) => url.href.includes(urlIncludes), { timeout })
        }
        return await statusWithTitle(tab)
      })

    case "resize":
      return await tabs.record(tab, "resize", async () => {
        const setting = resolveViewport(input)
        const size =
          setting._tag === "fill"
            ? FALLBACK_VIEWPORT
            : { width: setting.width, height: setting.height }
        await page.setViewportSize(size)
        tab.viewport = setting
        return { tabId: tab.id, setting, viewport: size }
      })

    case "setColorScheme":
      return await tabs.record(tab, "setColorScheme", async () => {
        const scheme = str(input, "colorScheme") ?? "system"
        await page.emulateMedia({
          colorScheme: scheme === "system" ? null : (scheme as "light" | "dark"),
        })
        return {}
      })

    case "snapshot":
      return await tabs.record(tab, "snapshot", async () => {
        const shot = await page.screenshot({ type: "png", timeout })
        const size = viewportOf(page)
        return {
          url: page.url(),
          title: await page.title().catch(() => ""),
          loading: false,
          visibleText: await page
            .evaluate(() => document.body?.innerText ?? "")
            .catch(() => ""),
          interactiveElements: await collectElements(page).catch(() => []),
          accessibilityTree: await accessibilityTree(page).catch(() => null),
          consoleEntries: [...tab.console],
          networkEntries: [...tab.network],
          actionTimeline: [...tab.timeline],
          screenshot: {
            mimeType: "image/png" as const,
            data: shot.toString("base64"),
            width: size.width,
            height: size.height,
          },
        }
      })

    default:
      throw new Error(`unsupported operation: ${operation}`)
  }
}

const accessibilityTree = async (page: Page): Promise<unknown> => {
  const session = await page.context().newCDPSession(page)
  try {
    return await session.send("Accessibility.getFullAXTree")
  } finally {
    await session.detach().catch(() => {})
  }
}

const environmentPort = (input: Input): string | undefined => {
  const target = input["target"]
  if (target === null || typeof target !== "object") return undefined
  const record = target as Input
  if (record["kind"] !== "environment-port") return undefined
  const port = num(record, "port")
  return port === undefined ? undefined : `http://127.0.0.1:${port}`
}

const resolveViewport = (input: Input): ViewportSetting => {
  const mode = str(input, "mode") ?? "fill"
  if (mode === "freeform") {
    const width = num(input, "width")
    const height = num(input, "height")
    if (width === undefined || height === undefined) {
      throw new Error("freeform resize requires width and height")
    }
    return { _tag: "freeform", width, height }
  }
  if (mode === "preset") {
    const presetId = str(input, "preset")
    if (presetId === undefined) throw new Error("preset resize requires preset")
    const deviceName = PRESET_DEVICES[presetId]
    const device = deviceName === undefined ? undefined : devices[deviceName]
    const size = device?.viewport ?? FALLBACK_VIEWPORT
    const landscape = str(input, "orientation") === "landscape"
    return {
      _tag: "preset",
      presetId,
      width: landscape ? size.height : size.width,
      height: landscape ? size.width : size.height,
    }
  }
  return { _tag: "fill" }
}

export const SUPPORTED_OPERATIONS = [
  "status",
  "open",
  "navigate",
  "snapshot",
  "click",
  "type",
  "press",
  "scroll",
  "evaluate",
  "waitFor",
  "resize",
  "setColorScheme",
] as const
