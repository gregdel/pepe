import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { unlink } from "node:fs/promises";
import { resolve } from "node:path";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

const tools = "gio_start,gio_restart,gio_stop,gio_status,gio_tree,gio_click,gio_type,gio_key,gio_resize,gio_screenshot";
const resultLimit = 16_000;
const stderrLimit = 4_000;
const reviewPrompt = `You are a read-only UI test agent for a Gio TV/remote interface. The implementation is already done by another agent. Do not modify source files or run shell commands. Use the Gio tools to start the headless app, inspect its semantic tree, navigate with directional keys (prefer semantic targets for pointer controls), resize, and take screenshots. Inspect actual screenshots visually; test at 1920x1080 and a smaller viewport when applicable. Test relevant happy paths and nearby navigation for regressions. Use gio_tree to confirm states instead of assuming key counts. The headless catalog may be empty without GIO_CONTROL_CONFIG or POLOCHON_URL/POLOCHON_TOKEN: report this as a coverage limit, not a pass. When finished, report PASS, FAIL, or INCONCLUSIVE for each check, exact reproduction steps, viewport and screenshot paths, and any untested cases. Do not claim a screenshot was inspected unless you used gio_screenshot.`;

function lastAnswer(events: string[]): { text: string; failure?: string } {
  let text = "";
  let failure: string | undefined;
  for (const line of events) {
    try {
      const event = JSON.parse(line);
      if (event.type !== "message_end" || event.message?.role !== "assistant") continue;
      const message = event.message;
      failure = message.stopReason === "error" || message.stopReason === "aborted"
        ? message.errorMessage ?? message.stopReason
        : undefined;
      const parts = message.content?.filter((part: { type: string }) => part.type === "text");
      if (parts?.length) text = parts.map((part: { text: string }) => part.text).join("\n");
    } catch {
      // Other stdout lines are not part of the JSON event stream.
    }
  }
  return { text, failure };
}

export default function (pi: ExtensionAPI) {
  let selectedModel: string | undefined;

  pi.on("session_start", (_event, ctx) => {
    selectedModel = undefined;
    for (const entry of ctx.sessionManager.getBranch()) {
      if (entry.type === "custom" && entry.customType === "ui-review-model") {
        const value = (entry.data as { model?: unknown })?.model;
        if (typeof value === "string") selectedModel = value;
      }
    }
  });

  pi.registerCommand("ui-review-model", {
    description: "Choose the vision model for the read-only Gio UI reviewer",
    handler: async (args, ctx) => {
      const available = (ctx.scopedModels.length ? ctx.scopedModels.map((item) => item.model) : ctx.modelRegistry.getAvailable())
        .filter((model) => model.input.includes("image") && ctx.modelRegistry.hasConfiguredAuth(model))
        .sort((a, b) => a.cost.output - b.cost.output || `${a.provider}/${a.id}`.localeCompare(`${b.provider}/${b.id}`));
      const ids = [...new Set(available.map((model) => `${model.provider}/${model.id}`))];
      if (ids.length === 0) {
        ctx.ui.notify("No authenticated image-capable models available", "error");
        return;
      }
      const requested = args.trim();
      if (!requested && !ctx.hasUI) {
        ctx.ui.notify("Specify provider/model: /ui-review-model provider/model", "error");
        return;
      }
      const choice = requested || await ctx.ui.select("UI reviewer model (cheapest output first)", ids);
      if (!choice) return;
      if (!ids.includes(choice)) {
        ctx.ui.notify(`Unavailable image-capable model: ${choice}`, "error");
        return;
      }
      selectedModel = choice;
      pi.appendEntry("ui-review-model", { model: choice });
      ctx.ui.notify(`UI reviewer: ${choice}`, "info");
    },
  });

  pi.registerTool({
    name: "ui_review",
    label: "UI review",
    description: "Delegate Gio UI navigation, screenshots, and regression review to the user-selected smaller vision model. Read-only; requires /ui-review-model first. Returns a bounded test report with screenshot paths.",
    promptSnippet: "Delegate Gio UI regression checks to the selected independent vision model",
    parameters: Type.Object({
      task: Type.String({ description: "What changed, which screens and behavior to check, and expected results" }),
    }),
    async execute(_id, params, signal, onUpdate, ctx) {
      if (!selectedModel) throw new Error("Choose a vision model first with /ui-review-model");
      const chosenModel = selectedModel;
      const [provider, ...idParts] = chosenModel.split("/");
      const model = ctx.modelRegistry.find(provider, idParts.join("/"));
      if (!model?.input.includes("image") || !ctx.modelRegistry.hasConfiguredAuth(model)) {
        throw new Error(`Selected UI reviewer model is no longer available: ${chosenModel}`);
      }
      if (signal?.aborted) throw new Error("UI review cancelled");

      // Load only the Gio extension in the child, not user extensions or this dispatcher.
      const gioExtension = resolve(ctx.cwd, ".pi/extensions/gio-control.ts");
      const args = ["--mode", "json", "-p", "--no-session", "--no-extensions", "-e", gioExtension,
        "--no-skills", "--no-prompt-templates", "--no-themes", "--model", chosenModel,
        "--thinking", "off", "--tools", tools, "--system-prompt", reviewPrompt,
        "--", `Review task: ${params.task}`];

      const runId = randomUUID();
      const binary = resolve(ctx.cwd, `.pi/tmp/gio-control-ui-review-${runId}`);
      const child = spawn("pi", args, {
        cwd: ctx.cwd,
        stdio: ["ignore", "pipe", "pipe"],
        env: {
          ...process.env,
          GIO_CONTROL_BIN: binary,
          GIO_CONTROL_SCREENSHOT_DIR: `.pi/tmp/screenshots/ui-review-${runId}`,
        },
      });
      const lines: string[] = [];
      let sawScreenshot = false;
      let pending = "";
      let stderr = "";
      let killed = false;
      let forceKill: ReturnType<typeof setTimeout> | undefined;
      const abort = () => {
        killed = true;
        child.kill("SIGTERM");
        forceKill = setTimeout(() => { if (child.exitCode === null) child.kill("SIGKILL"); }, 5_000);
        forceKill.unref();
      };
      signal?.addEventListener("abort", abort, { once: true });
      if (signal?.aborted) abort();

      child.stdout.setEncoding("utf8");
      child.stdout.on("data", (chunk: string) => {
        pending += chunk;
        const records = pending.split("\n");
        pending = records.pop() ?? "";
        for (const record of records) {
          // Keep only assistant messages; tool results can contain multi-MB base64 PNGs.
          try {
            const event = JSON.parse(record);
            if (event.type !== "message_end" || event.message?.role !== "assistant") continue;
            lines.push(record);
            if (lines.length > 100) lines.shift();
            const call = event.message.content?.find((part: { type: string }) => part.type === "toolCall");
            if (call?.name === "gio_screenshot") sawScreenshot = true;
            if (call) onUpdate?.({ content: [{ type: "text", text: `UI reviewer: ${call.name}` }] });
          } catch { /* ignore malformed events */ }
        }
      });
      child.stderr.setEncoding("utf8");
      child.stderr.on("data", (chunk: string) => { stderr = (stderr + chunk).slice(-stderrLimit); });

      try {
        const exitCode = await new Promise<number>((resolvePromise, reject) => {
          child.on("error", reject);
          child.on("close", (code) => resolvePromise(code ?? 1));
        });
        if (killed) throw new Error("UI review cancelled");
        const { text, failure } = lastAnswer(lines);
        if (exitCode !== 0 || failure || !text.trim()) {
          throw new Error(`UI reviewer failed (${chosenModel}, exit ${exitCode}): ${failure || stderr.trim() || "no report"}`);
        }
        if (!sawScreenshot) throw new Error("UI review incomplete: reviewer did not request a screenshot");
        const output = text.length > resultLimit ? `${text.slice(0, resultLimit)}\n[Report truncated]` : text;
        return { content: [{ type: "text", text: `UI review by ${chosenModel}:\n${output}` }], details: { model: chosenModel } };
      } finally {
        signal?.removeEventListener("abort", abort);
        if (forceKill) clearTimeout(forceKill);
        await unlink(binary).catch(() => {});
      }
    },
  });
}
