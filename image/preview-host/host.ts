import { Socket } from "@effect/platform"
import { Rpc, RpcClient, RpcGroup, RpcSerialization } from "@effect/rpc"
import { NodeRuntime } from "@effect/platform-node"
import { Effect, Layer, Schedule, Schema, Stream } from "effect"
import { chromium } from "playwright-core"
import { WebSocket } from "ws"
import { load } from "./config.ts"
import type { Config } from "./config.ts"
import { SUPPORTED_OPERATIONS, dispatch } from "./operations.ts"
import { TabRegistry } from "./tabs.ts"

const AutomationRequest = Schema.Struct({
  requestId: Schema.String,
  threadId: Schema.String,
  tabId: Schema.optional(Schema.NullOr(Schema.String)),
  tabIdExplicit: Schema.optional(Schema.NullOr(Schema.Boolean)),
  operation: Schema.String,
  input: Schema.Unknown,
  timeoutMs: Schema.Number,
})

const StreamEvent = Schema.Union(
  Schema.Struct({ type: Schema.Literal("connected"), connectionId: Schema.String }),
  Schema.Struct({
    type: Schema.Literal("request"),
    connectionId: Schema.String,
    request: AutomationRequest,
  }),
)

const PreviewAutomation = RpcGroup.make(
  Rpc.make("previewAutomation.connect", {
    payload: {
      clientId: Schema.String,
      environmentId: Schema.String,
      supportedOperations: Schema.optional(Schema.Array(Schema.String)),
    },
    success: StreamEvent,
    stream: true,
  }),
  Rpc.make("previewAutomation.respond", {
    payload: {
      clientId: Schema.String,
      connectionId: Schema.String,
      requestId: Schema.String,
      ok: Schema.Boolean,
      result: Schema.optional(Schema.Unknown),
      error: Schema.optional(
        Schema.Struct({
          _tag: Schema.String,
          message: Schema.String,
          detail: Schema.optional(Schema.Unknown),
        }),
      ),
    },
  }),
)

const socketUrl = (config: Config): string => {
  const url = new URL("/ws", config.origin)
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:"
  return url.toString()
}

// t3 issues browser clients a ticket because a browser WebSocket cannot set
// headers. A headless host can, so it authenticates on the upgrade directly.
const webSocketConstructor = (config: Config) =>
  Layer.succeed(Socket.WebSocketConstructor, (url, protocols) =>
    new WebSocket(url, protocols, {
      headers: { authorization: `Bearer ${config.token}` },
    }) as unknown as globalThis.WebSocket,
  )

const session = (config: Config) =>
    Effect.gen(function* () {
      yield* Effect.logInfo("launching the browser baked into the image")
      const browser = yield* Effect.acquireRelease(
        Effect.promise(() =>
          chromium.launch({
            args: ["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"],
          }),
        ),
        (open) => Effect.promise(() => open.close()),
      )
    const tabs = new TabRegistry(browser)
    yield* Effect.logInfo(`browser ready; opening ${socketUrl(config)}`)

    const client = yield* RpcClient.make(PreviewAutomation)

    let connectionId: string | null = null

    yield* client.previewAutomation.connect(
      {
        clientId: config.clientId,
        environmentId: config.environmentId,
        supportedOperations: [...SUPPORTED_OPERATIONS],
      },
    ).pipe(
      Stream.tapError((cause) => Effect.logError(`stream error: ${JSON.stringify(cause)}`)),
      Stream.runForEach((event) =>
        Effect.gen(function* () {
          if (event.type === "connected") {
            connectionId = event.connectionId
            yield* Effect.logInfo(`preview host connected as ${config.clientId}`)
            return
          }
          const active = connectionId ?? event.connectionId
          const { request } = event
          const outcome = yield* Effect.tryPromise({
            try: () =>
            dispatch(
              tabs,
              request.operation,
              request.tabId === null || request.tabId === undefined
                ? request.input
                : { ...(request.input as Record<string, unknown>), tabId: request.tabId },
              request.timeoutMs,
            ),
            catch: (cause) => cause,
          }).pipe(Effect.either)

          yield* client.previewAutomation.respond(
            outcome._tag === "Right"
              ? {
                  clientId: config.clientId,
                  connectionId: active,
                  requestId: request.requestId,
                  ok: true,
                  result: outcome.right,
                }
              : {
                  clientId: config.clientId,
                  connectionId: active,
                  requestId: request.requestId,
                  ok: false,
                  error: {
                    _tag: "PreviewAutomationHostError",
                    message:
                      outcome.left instanceof Error
                        ? outcome.left.message
                        : String(outcome.left),
                  },
                },
          )
        }),
      ),
    )
  })

const protocolLayer = (config: Config) =>
  RpcClient.layerProtocolSocket().pipe(
    Layer.provide(Socket.layerWebSocket(socketUrl(config))),
    Layer.provide(webSocketConstructor(config)),
    Layer.provide(RpcSerialization.layerJson),
  )

const backoff = Schedule.exponential("1 second").pipe(
  Schedule.union(Schedule.spaced("30 seconds")),
)

const main = Effect.gen(function* () {
  const config = yield* Effect.promise(load)
  yield* session(config).pipe(
    Effect.provide(protocolLayer(config)),
    Effect.scoped,
    Effect.tapError((cause) => Effect.logError(`preview host disconnected: ${cause}`)),
    Effect.retry(backoff),
  )
})

NodeRuntime.runMain(main)
