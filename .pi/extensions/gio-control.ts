import type { ChildProcessWithoutNullStreams } from "node:child_process";
import { spawn } from "node:child_process";
import { mkdir, readFile } from "node:fs/promises";
import { dirname, isAbsolute, resolve } from "node:path";
import { createInterface } from "node:readline";

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { StringEnum } from "@earendil-works/pi-ai";
import { Type } from "typebox";

const DRIVER_PACKAGE = process.env.GIO_CONTROL_PACKAGE ?? "./cmd/gio-control";
const DRIVER_BIN = process.env.GIO_CONTROL_BIN ?? ".pi/tmp/gio-control";
const START_TIMEOUT_MS = parsePositiveInt(process.env.GIO_CONTROL_START_TIMEOUT_MS, 20_000);
const RPC_TIMEOUT_MS = parsePositiveInt(process.env.GIO_CONTROL_RPC_TIMEOUT_MS, 10_000);
const STOP_GRACE_MS = 1_000;
const STDERR_LIMIT = 16 * 1024;
const TREE_TEXT_LIMIT = 64 * 1024;

type JsonObject = Record<string, unknown>;

type ReadyEvent = {
  event: "ready";
  pid?: number;
  width?: number;
  height?: number;
  [key: string]: unknown;
};

type RpcResponse = {
  id: number;
  result?: unknown;
  error?: string | { message?: string; [key: string]: unknown };
};

type PendingRequest = {
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
  timer: ReturnType<typeof setTimeout>;
};

type DriverStatus = {
  running: boolean;
  pid?: number;
  cwd?: string;
  generation: number;
  ready?: ReadyEvent;
  stderrTail?: string;
  mockMpv: boolean;
};

function parsePositiveInt(value: string | undefined, fallback: number): number {
  if (value === undefined) return fallback;
  const n = Number.parseInt(value, 10);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}

function errorMessage(value: unknown): string {
  if (value instanceof Error) return value.message;
  return String(value);
}

function responseError(value: RpcResponse["error"]): string {
  if (typeof value === "string") return value;
  if (value && typeof value.message === "string") return value.message;
  return JSON.stringify(value ?? "unknown driver error");
}

function textResult(text: string, details: unknown = {}) {
  return {
    content: [{ type: "text" as const, text }],
    details,
  };
}

function jsonForModel(value: unknown, limit = TREE_TEXT_LIMIT): string {
  const text = JSON.stringify(value, null, 2) ?? String(value);
  if (text.length <= limit) return text;
  return `${text.slice(0, limit)}\n\n[truncated at ${limit} characters]`;
}

function runProcess(
  command: string,
  args: string[],
  options: { cwd: string; signal?: AbortSignal; timeoutMs?: number },
): Promise<{ stdout: string; stderr: string }> {
  return new Promise((resolvePromise, reject) => {
    if (options.signal?.aborted) {
      reject(new Error("cancelled"));
      return;
    }

    const child = spawn(command, args, {
      cwd: options.cwd,
      stdio: ["ignore", "pipe", "pipe"],
      env: process.env,
    });

    let stdout = "";
    let stderr = "";
    let settled = false;

    const finish = (err?: Error) => {
      if (settled) return;
      settled = true;
      if (timer) clearTimeout(timer);
      options.signal?.removeEventListener("abort", onAbort);
      if (err) reject(err);
      else resolvePromise({ stdout, stderr });
    };

    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => {
      stdout += chunk;
    });
    child.stderr.on("data", (chunk: string) => {
      stderr += chunk;
    });

    child.on("error", (err) => finish(err));
    child.on("exit", (code, signal) => {
      if (code === 0) {
        finish();
        return;
      }
      const suffix = stderr.trim() ? `\n${stderr.trim()}` : "";
      finish(new Error(`${command} exited with code ${code ?? "?"}${signal ? ` (${signal})` : ""}${suffix}`));
    });

    const onAbort = () => {
      child.kill("SIGTERM");
      finish(new Error("cancelled"));
    };
    options.signal?.addEventListener("abort", onAbort, { once: true });

    const timer = options.timeoutMs
      ? setTimeout(() => {
          child.kill("SIGTERM");
          finish(new Error(`${command} timed out after ${options.timeoutMs} ms`));
        }, options.timeoutMs)
      : undefined;
  });
}

class DriverManager {
  private child?: ChildProcessWithoutNullStreams;
  private cwd?: string;
  private ready?: ReadyEvent;
  private generation = 0;
  private nextId = 1;
  private pending = new Map<number, PendingRequest>();
  private stderrTail = "";
  private startPromise?: Promise<ReadyEvent>;
  private mockMpv = false;
  private resolveReady?: (ready: ReadyEvent) => void;
  private rejectReady?: (error: Error) => void;

  status(): DriverStatus {
    return {
      running: this.isRunning(),
      pid: this.child?.pid,
      cwd: this.cwd,
      generation: this.generation,
      ready: this.ready,
      stderrTail: this.stderrTail || undefined,
      mockMpv: this.mockMpv,
    };
  }

  isRunning(): boolean {
    return this.child !== undefined && this.child.exitCode === null && !this.child.killed;
  }

  async ensure(cwd: string, signal?: AbortSignal): Promise<ReadyEvent> {
    if (this.isRunning() && this.cwd === cwd && this.ready) return this.ready;
    return this.start(cwd, signal);
  }

  async start(cwd: string, signal?: AbortSignal, mockMpv = this.mockMpv): Promise<ReadyEvent> {
    if (this.isRunning() && this.cwd === cwd && this.ready && this.mockMpv === mockMpv) return this.ready;
    if (this.startPromise) return this.startPromise;

    this.startPromise = this.startImpl(cwd, signal, mockMpv);
    try {
      return await this.startPromise;
    } finally {
      this.startPromise = undefined;
    }
  }

  private async startImpl(cwd: string, signal?: AbortSignal, mockMpv = false): Promise<ReadyEvent> {
    await this.stop();

    const binPath = isAbsolute(DRIVER_BIN) ? DRIVER_BIN : resolve(cwd, DRIVER_BIN);
    await mkdir(dirname(binPath), { recursive: true });

    await runProcess("go", ["build", "-o", binPath, DRIVER_PACKAGE], {
      cwd,
      signal,
      timeoutMs: START_TIMEOUT_MS,
    });

    if (signal?.aborted) throw new Error("cancelled");

    this.cwd = cwd;
    this.mockMpv = mockMpv;
    this.ready = undefined;
    this.stderrTail = "";

    const child = spawn(binPath, [], {
      cwd,
      stdio: ["pipe", "pipe", "pipe"],
      env: { ...process.env, GIO_CONTROL_MOCK_MPV: mockMpv ? "1" : "0" },
    });
    this.child = child;

    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");

    const lines = createInterface({ input: child.stdout, crlfDelay: Infinity });
    lines.on("line", (line) => this.handleLine(line));

    child.stderr.on("data", (chunk: string) => {
      this.stderrTail = (this.stderrTail + chunk).slice(-STDERR_LIMIT);
    });

    child.on("error", (err) => this.handleExit(new Error(`driver spawn failed: ${err.message}`)));
    child.on("exit", (code, sig) => {
      const stderr = this.stderrTail.trim();
      const message = `driver exited with code ${code ?? "?"}${sig ? ` (${sig})` : ""}${stderr ? `\n${stderr}` : ""}`;
      this.handleExit(new Error(message));
    });

    const readyPromise = new Promise<ReadyEvent>((resolvePromise, reject) => {
      this.resolveReady = resolvePromise;
      this.rejectReady = reject;
    });

    let timeout: ReturnType<typeof setTimeout> | undefined;
    const timedReady = new Promise<ReadyEvent>((resolvePromise, reject) => {
      timeout = setTimeout(() => {
        reject(new Error(`driver did not emit a ready event within ${START_TIMEOUT_MS} ms${this.stderrSuffix()}`));
      }, START_TIMEOUT_MS);
      readyPromise.then(resolvePromise, reject);
    });

    const onAbort = () => {
      this.rejectReady?.(new Error("cancelled"));
      child.kill("SIGTERM");
    };
    signal?.addEventListener("abort", onAbort, { once: true });

    try {
      const ready = await timedReady;
      this.ready = ready;
      this.generation += 1;
      return ready;
    } catch (err) {
      await this.stop();
      throw err;
    } finally {
      if (timeout) clearTimeout(timeout);
      signal?.removeEventListener("abort", onAbort);
      this.resolveReady = undefined;
      this.rejectReady = undefined;
    }
  }

  async restart(cwd: string, signal?: AbortSignal): Promise<ReadyEvent> {
    await this.stop();
    return this.start(cwd, signal, this.mockMpv);
  }

  async stop(): Promise<void> {
    const child = this.child;
    if (!child) {
      this.resetProcessState();
      return;
    }

    // Prefer protocol shutdown so the Go side can Release() graphics resources.
    if (this.isRunning() && this.ready) {
      try {
        await this.request("shutdown", {}, undefined, Math.min(RPC_TIMEOUT_MS, STOP_GRACE_MS));
      } catch {
        // Fall through to signals.
      }
    }

    if (child.exitCode === null) {
      child.kill("SIGTERM");
      await Promise.race([
        new Promise<void>((resolvePromise) => child.once("exit", () => resolvePromise())),
        new Promise<void>((resolvePromise) => setTimeout(resolvePromise, STOP_GRACE_MS)),
      ]);
    }

    if (child.exitCode === null) {
      child.kill("SIGKILL");
    }

    this.handleExit(new Error("driver stopped"));
  }

  async request(
    method: string,
    params: JsonObject = {},
    signal?: AbortSignal,
    timeoutMs = RPC_TIMEOUT_MS,
  ): Promise<unknown> {
    const child = this.child;
    if (!child || !this.isRunning()) throw new Error("UI driver is not running");
    if (signal?.aborted) throw new Error("cancelled");

    const id = this.nextId++;
    const request = JSON.stringify({ id, method, params }) + "\n";

    return new Promise((resolvePromise, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`UI driver request ${method} timed out after ${timeoutMs} ms${this.stderrSuffix()}`));
      }, timeoutMs);

      this.pending.set(id, { resolve: resolvePromise, reject, timer });

      child.stdin.write(request, "utf8", (err) => {
        if (!err) return;
        const pending = this.pending.get(id);
        if (!pending) return;
        clearTimeout(pending.timer);
        this.pending.delete(id);
        pending.reject(new Error(`failed to write request to UI driver: ${err.message}`));
      });
    });
  }

  private handleLine(line: string): void {
    let message: unknown;
    try {
      message = JSON.parse(line);
    } catch {
      this.handleExit(new Error(`UI driver wrote non-JSON data to stdout: ${line.slice(0, 500)}`));
      this.child?.kill("SIGTERM");
      return;
    }

    if (!message || typeof message !== "object") return;
    const object = message as Record<string, unknown>;

    if (object.event === "ready") {
      const ready = object as ReadyEvent;
      this.ready = ready;
      this.resolveReady?.(ready);
      return;
    }

    if (typeof object.id !== "number") return;
    const response = object as RpcResponse;
    const pending = this.pending.get(response.id);
    if (!pending) return;

    clearTimeout(pending.timer);
    this.pending.delete(response.id);

    if (response.error !== undefined) {
      pending.reject(new Error(responseError(response.error)));
    } else {
      pending.resolve(response.result);
    }
  }

  private handleExit(error: Error): void {
    this.rejectReady?.(error);
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    this.pending.clear();
    this.resetProcessState();
  }

  private resetProcessState(): void {
    this.child = undefined;
    this.cwd = undefined;
    this.ready = undefined;
  }

  private stderrSuffix(): string {
    const stderr = this.stderrTail.trim();
    return stderr ? `\nDriver stderr:\n${stderr}` : "";
  }
}

function assertObject(value: unknown, what: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${what} returned an invalid response`);
  }
  return value as Record<string, unknown>;
}

export default function gioUiExtension(pi: ExtensionAPI) {
  const driver = new DriverManager();

  pi.on("session_shutdown", async () => {
    await driver.stop();
  });

  pi.registerTool({
    name: "gio_start",
    label: "Start Gio UI",
    description: "Build and start the headless Gio UI driver. The driver is also started automatically by UI interaction tools.",
    promptSnippet: "Build and start the headless Gio UI test instance",
    executionMode: "sequential",
    parameters: Type.Object({
      width: Type.Optional(Type.Integer({ minimum: 1 })),
      height: Type.Optional(Type.Integer({ minimum: 1 })),
      mockMpv: Type.Optional(Type.Boolean({ description: "Use an offline two-movie catalog and mock mpv process instead of server configuration; never play actual media." })),
    }),
    async execute(_id, params, signal, _onUpdate, ctx) {
      const ready = await driver.start(ctx.cwd, signal, params.mockMpv ?? false);
      if (params.width !== undefined || params.height !== undefined) {
        if (params.width === undefined || params.height === undefined) {
          throw new Error("width and height must be supplied together");
        }
        await driver.request("resize", { width: params.width, height: params.height }, signal);
      }
      return textResult(jsonForModel({ ...driver.status(), ready }));
    },
  });

  pi.registerTool({
    name: "gio_restart",
    label: "Restart Gio UI",
    description: "Rebuild and restart the headless Gio UI driver after source changes.",
    promptSnippet: "Rebuild and restart the headless Gio UI test instance",
    promptGuidelines: ["Use gio_restart after changing Gio UI code before inspecting the new UI."],
    executionMode: "sequential",
    parameters: Type.Object({}),
    async execute(_id, _params, signal, _onUpdate, ctx) {
      const ready = await driver.restart(ctx.cwd, signal);
      return textResult(jsonForModel({ ...driver.status(), ready }));
    },
  });

  pi.registerTool({
    name: "gio_stop",
    label: "Stop Gio UI",
    description: "Stop the current headless Gio UI driver instance.",
    executionMode: "sequential",
    parameters: Type.Object({}),
    async execute() {
      await driver.stop();
      return textResult("Gio UI driver stopped.");
    },
  });

  pi.registerTool({
    name: "gio_status",
    label: "Gio UI Status",
    description: "Report whether the headless Gio UI driver is running and show recent driver stderr.",
    executionMode: "sequential",
    parameters: Type.Object({}),
    async execute() {
      return textResult(jsonForModel(driver.status()));
    },
  });

  pi.registerTool({
    name: "gio_tree",
    label: "Inspect Gio UI",
    description: "Return the current Gio semantic/accessibility tree. Starts the headless driver automatically if necessary.",
    promptSnippet: "Inspect the semantic tree of the headless Gio UI",
    promptGuidelines: [
      "Prefer gio_tree and semantic targets over screenshot-coordinate guessing when interacting with Gio UI controls.",
    ],
    executionMode: "sequential",
    parameters: Type.Object({}),
    async execute(_id, _params, signal, _onUpdate, ctx) {
      await driver.ensure(ctx.cwd, signal);
      const tree = await driver.request("tree", {}, signal);
      return textResult(jsonForModel(tree));
    },
  });

  pi.registerTool({
    name: "gio_click",
    label: "Click Gio UI",
    description: "Click a Gio UI element by semantic label/id or by explicit headless-window coordinates.",
    promptSnippet: "Click a semantic Gio UI element",
    promptGuidelines: ["Prefer gio_click label or id targets; use x/y only when the semantic tree cannot identify the target."],
    executionMode: "sequential",
    parameters: Type.Object({
      label: Type.Optional(Type.String({ description: "Semantic label to click." })),
      id: Type.Optional(Type.Integer({ description: "Semantic node id to click." })),
      occurrence: Type.Optional(Type.Integer({ minimum: 1, description: "1-based occurrence when labels are duplicated." })),
      x: Type.Optional(Type.Number({ description: "Headless-window x coordinate." })),
      y: Type.Optional(Type.Number({ description: "Headless-window y coordinate." })),
      button: Type.Optional(StringEnum(["primary", "secondary", "tertiary"] as const)),
    }),
    async execute(_toolCallId, params, signal, _onUpdate, ctx) {
      const hasSemanticTarget = params.label !== undefined || params.id !== undefined;
      const hasCoordinateTarget = params.x !== undefined || params.y !== undefined;
      if (!hasSemanticTarget && !hasCoordinateTarget) {
        throw new Error("specify label, id, or x/y");
      }
      if (hasCoordinateTarget && (params.x === undefined || params.y === undefined)) {
        throw new Error("x and y must be supplied together");
      }

      await driver.ensure(ctx.cwd, signal);
      const result = await driver.request("click", { ...params }, signal);
      return textResult(jsonForModel(result ?? { ok: true }));
    },
  });

  pi.registerTool({
    name: "gio_type",
    label: "Type in Gio UI",
    description: "Insert text into the currently focused Gio editor using the headless Gio input router.",
    promptSnippet: "Type text into the focused Gio editor",
    executionMode: "sequential",
    parameters: Type.Object({
      text: Type.String(),
    }),
    async execute(_id, params, signal, _onUpdate, ctx) {
      await driver.ensure(ctx.cwd, signal);
      const result = await driver.request("type", { text: params.text }, signal);
      return textResult(jsonForModel(result ?? { ok: true }));
    },
  });

  pi.registerTool({
    name: "gio_key",
    label: "Send Gio Key",
    description: "Send a key event to the headless Gio UI. Key names are interpreted by the Go driver.",
    executionMode: "sequential",
    parameters: Type.Object({
      name: Type.String({ description: "Gio key name, for example Return, Tab, Escape, UpArrow." }),
      modifiers: Type.Optional(Type.Array(StringEnum(["Ctrl", "Shift", "Alt", "Super"] as const), { uniqueItems: true })),
    }),
    async execute(_id, params, signal, _onUpdate, ctx) {
      await driver.ensure(ctx.cwd, signal);
      const result = await driver.request("key", { name: params.name, modifiers: params.modifiers ?? [] }, signal);
      return textResult(jsonForModel(result ?? { ok: true }));
    },
  });

  pi.registerTool({
    name: "gio_resize",
    label: "Resize Gio UI",
    description: "Resize the headless Gio UI viewport and render a new frame.",
    executionMode: "sequential",
    parameters: Type.Object({
      width: Type.Integer({ minimum: 1 }),
      height: Type.Integer({ minimum: 1 }),
    }),
    async execute(_id, params, signal, _onUpdate, ctx) {
      await driver.ensure(ctx.cwd, signal);
      const result = await driver.request("resize", { width: params.width, height: params.height }, signal);
      return textResult(jsonForModel(result ?? { width: params.width, height: params.height }));
    },
  });

  pi.registerTool({
    name: "gio_screenshot",
    label: "Screenshot Gio UI",
    description: "Capture the actual headless Gio render and return it to the model as a PNG image. Waits up to 8s for library and artwork loading first.",
    promptSnippet: "Capture and visually inspect the headless Gio UI",
    executionMode: "sequential",
    parameters: Type.Object({}),
    async execute(_id, _params, signal, _onUpdate, ctx) {
      await driver.ensure(ctx.cwd, signal);
      const raw = await driver.request("screenshot", {}, signal);
      const result = assertObject(raw, "screenshot");

      let data: string;
      let path: string | undefined;

      if (typeof result.pngBase64 === "string") {
        data = result.pngBase64;
      } else if (typeof result.path === "string") {
        path = isAbsolute(result.path) ? result.path : resolve(ctx.cwd, result.path);
        data = (await readFile(path)).toString("base64");
      } else {
        throw new Error("screenshot response must contain pngBase64 or path");
      }

      const width = typeof result.width === "number" ? result.width : undefined;
      const height = typeof result.height === "number" ? result.height : undefined;
      const loading = result.loading === true;
      const description = `Gio screenshot${width && height ? ` (${width}x${height})` : ""}${path ? ` from ${path}` : ""}${loading ? " (artwork was still loading after waiting; take another screenshot)" : ""}`;

      return {
        content: [
          { type: "text" as const, text: description },
          { type: "image" as const, data, mimeType: "image/png" },
        ],
        details: { width, height, path, loading },
      };
    },
  });
}
