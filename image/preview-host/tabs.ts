import type { Browser, BrowserContext, Page } from "playwright-core"
import { randomUUID } from "node:crypto"

const BUFFER_LIMIT = 200

export type ConsoleEntry = {
  level: string
  text: string
  timestamp: string
  source?: string
}

export type NetworkEntry = {
  url: string
  method: string
  status: number | null
  failed: boolean
  errorText?: string
  timestamp: string
}

export type ActionEvent = {
  id: string
  action: string
  status: "running" | "succeeded" | "failed" | "interrupted"
  startedAt: string
  completedAt?: string
  error?: string
}

export type ViewportSetting =
  | { _tag: "fill" }
  | { _tag: "freeform"; width: number; height: number }
  | { _tag: "preset"; width: number; height: number; presetId: string }

export type Tab = {
  readonly id: string
  readonly page: Page
  readonly console: ConsoleEntry[]
  readonly network: NetworkEntry[]
  readonly timeline: ActionEvent[]
  viewport: ViewportSetting
}

const push = <T>(buffer: T[], entry: T): void => {
  buffer.push(entry)
  if (buffer.length > BUFFER_LIMIT) buffer.splice(0, buffer.length - BUFFER_LIMIT)
}

const now = (): string => new Date().toISOString()

export class TabRegistry {
  readonly #tabs = new Map<string, Tab>()
  #current: string | null = null
  #context: BrowserContext | null = null

  readonly #browser: Browser

  constructor(browser: Browser) {
    this.#browser = browser
  }

  async #ensureContext(): Promise<BrowserContext> {
    if (this.#context !== null) return this.#context
    const existing = this.#browser.contexts()
    this.#context = existing[0] ?? (await this.#browser.newContext())
    return this.#context
  }

  #instrument(tab: Tab): void {
    tab.page.on("console", (message) => {
      push(tab.console, {
        level: message.type(),
        text: message.text(),
        timestamp: now(),
        source: message.location().url || undefined,
      })
    })
    tab.page.on("pageerror", (error) => {
      push(tab.console, { level: "error", text: error.message, timestamp: now() })
    })
    tab.page.on("response", (response) => {
      push(tab.network, {
        url: response.url(),
        method: response.request().method(),
        status: response.status(),
        failed: false,
        timestamp: now(),
      })
    })
    tab.page.on("requestfailed", (request) => {
      push(tab.network, {
        url: request.url(),
        method: request.method(),
        status: null,
        failed: true,
        errorText: request.failure()?.errorText,
        timestamp: now(),
      })
    })
    tab.page.on("close", () => {
      this.#tabs.delete(tab.id)
      if (this.#current === tab.id) this.#current = null
    })
  }

  async open(): Promise<Tab> {
    const context = await this.#ensureContext()
    const page = await context.newPage()
    const tab: Tab = {
      id: randomUUID(),
      page,
      console: [],
      network: [],
      timeline: [],
      viewport: { _tag: "fill" },
    }
    this.#tabs.set(tab.id, tab)
    this.#instrument(tab)
    this.#current = tab.id
    return tab
  }

  get(id: string): Tab | undefined {
    return this.#tabs.get(id)
  }

  current(): Tab | undefined {
    return this.#current === null ? undefined : this.#tabs.get(this.#current)
  }

  focus(id: string): void {
    if (this.#tabs.has(id)) this.#current = id
  }

  async resolve(id: string | undefined): Promise<Tab> {
    if (id !== undefined) {
      const tab = this.#tabs.get(id)
      if (tab === undefined) throw new Error(`no such tab: ${id}`)
      this.#current = id
      return tab
    }
    return this.current() ?? (await this.open())
  }

  async record<T>(tab: Tab, action: string, run: () => Promise<T>): Promise<T> {
    const event: ActionEvent = {
      id: randomUUID(),
      action,
      status: "running",
      startedAt: now(),
    }
    push(tab.timeline, event)
    try {
      const value = await run()
      event.status = "succeeded"
      event.completedAt = now()
      return value
    } catch (cause) {
      event.status = "failed"
      event.completedAt = now()
      event.error = cause instanceof Error ? cause.message : String(cause)
      throw cause
    }
  }
}
