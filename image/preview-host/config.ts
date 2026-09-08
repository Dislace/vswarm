import { readFile } from "node:fs/promises"
import { hostname } from "node:os"
import { homedir } from "node:os"
import { join } from "node:path"

export type Config = {
  readonly origin: string
  readonly token: string
  readonly environmentId: string
  readonly clientId: string
}

const readEnvFile = async (path: string): Promise<Map<string, string>> => {
  const entries = new Map<string, string>()
  let raw: string
  try {
    raw = await readFile(path, "utf8")
  } catch {
    return entries
  }
  for (const line of raw.split("\n")) {
    const trimmed = line.trim()
    if (trimmed === "" || trimmed.startsWith("#")) continue
    const eq = trimmed.indexOf("=")
    if (eq <= 0) continue
    entries.set(trimmed.slice(0, eq), trimmed.slice(eq + 1))
  }
  return entries
}

export const load = async (): Promise<Config> => {
  const home = homedir()
  const t3Home = process.env.T3CODE_HOME ?? join(home, ".config", "t3")

  const environmentId = (
    await readFile(join(t3Home, "userdata", "environment-id"), "utf8")
  ).trim()
  if (environmentId === "") throw new Error("environment-id is empty")

  const hostEnv = await readEnvFile(join(home, ".preview-host.env"))
  const token = process.env.T3_PREVIEW_HOST_TOKEN ?? hostEnv.get("T3_PREVIEW_HOST_TOKEN")
  if (token === undefined || token === "") {
    throw new Error(
      "T3_PREVIEW_HOST_TOKEN is unset and ~/.preview-host.env does not supply it",
    )
  }

  return {
    origin:
      process.env.T3_PREVIEW_HOST_ORIGIN ??
      hostEnv.get("T3_PREVIEW_HOST_ORIGIN") ??
      "http://127.0.0.1:3773",
    token,
    environmentId,
    clientId: process.env.T3_PREVIEW_HOST_CLIENT_ID ?? `vswarm-preview-host-${hostname()}`,
  }
}
