/**
 * Pi extension exposing linctl as a single `linear` tool.
 *
 * Design: one tool that wraps the linctl argv rather than one typed tool per
 * Linear operation. linctl already owns auth, the GraphQL surface, and JSON
 * output, and its SKILL.md teaches the model the command map, so duplicating
 * the flag surface in TypeBox schemas would drift the moment a flag is added.
 * What this tool adds over calling linctl through bash:
 *
 * - A named tool in the system prompt, so the model reaches for linctl
 *   deliberately instead of guessing shell invocations.
 * - `--json --plaintext` forced on every call, so output is parseable and no
 *   interactive prompt can hang the agent.
 * - A single choke point for destructive Linear writes: subcommands that
 *   delete or detach data require the user to confirm in the pi UI, and are
 *   refused when no UI is present. Additive writes (create, update, comment,
 *   assign) run without a prompt: they are visible in Linear and reversible,
 *   and the operator chose fewer interruptions over a gate on each one.
 * - `auth` is never reachable from the agent. Credentials are configured by
 *   the operator with `linctl auth` in a terminal.
 *
 * The linctl binary must be on PATH. sync-agents installs it to ~/.local/bin.
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { DEFAULT_MAX_BYTES, DEFAULT_MAX_LINES, formatSize, truncateHead } from "@earendil-works/pi-coding-agent";
import { Text } from "@earendil-works/pi-tui";
import { Type } from "typebox";

/**
 * linctl subcommand verbs that destroy or detach Linear data. Any occurrence
 * anywhere in argv marks the call as destructive, so `issue relation remove`
 * and `comment delete` match through their leaf verb. Raw GraphQL mutations
 * (`mcp call mutation.*`) also match, since their effect cannot be classified
 * from the argv.
 */
const DESTRUCTIVE_VERBS = new Set(["delete", "archive", "remove"]);

/** Forced on every call. Global linctl flags, valid after any subcommand. */
const FORCED_FLAGS = ["--json", "--plaintext"];

const TIMEOUT_MS = 60_000;

/** Reports whether a linctl argv performs a destructive write. */
export function isDestructive(args: string[]): boolean {
	return args.some((a) => DESTRUCTIVE_VERBS.has(a) || a.startsWith("mutation."));
}

export default function (pi: ExtensionAPI) {
	pi.registerTool({
		name: "linear",
		label: "Linear",
		description:
			"Run linctl against Linear. Pass the argv without the binary name, for example " +
			'["issue","get","ENG-80"] or ["issue","list","--assignee","me"]. ' +
			`${FORCED_FLAGS.join(" ")} are appended to every call, so output is JSON. ` +
			"Destructive writes (delete, archive, remove, raw mutations) ask the user to confirm; other writes run directly. " +
			"The auth subcommand is not available. " +
			`Output is truncated to ${DEFAULT_MAX_LINES} lines or ${formatSize(DEFAULT_MAX_BYTES)}; narrow the query with filters or --limit when that happens. ` +
			"See the linctl skill for the command map, default filters, and gotchas.",
		promptSnippet: "Read and update Linear issues, projects, comments, labels, and teams through linctl",
		promptGuidelines: [
			"Use the linear tool, not bash, for any linctl invocation.",
			"Before a write with the linear tool, read the current state of the target with a get or list call.",
		],
		parameters: Type.Object({
			args: Type.Array(Type.String(), {
				description: "linctl argv excluding the binary, for example [\"issue\",\"get\",\"ENG-80\"]",
			}),
		}),

		async execute(_toolCallId, params, signal, _onUpdate, ctx: ExtensionContext) {
			const args = params.args;
			if (args.length === 0) {
				throw new Error("linear: args is empty; pass a linctl subcommand such as [\"issue\",\"list\"]");
			}
			if (args[0] === "auth") {
				throw new Error("linear: the auth subcommand is operator-only; run `linctl auth` in a terminal");
			}
			const command = `linctl ${args.join(" ")}`;
			if (isDestructive(args)) {
				if (!ctx.hasUI) {
					throw new Error(`linear: ${command} is destructive and no UI is available to confirm it`);
				}
				const ok = await ctx.ui.confirm("Linear destructive write", command);
				if (!ok) {
					throw new Error(`linear: user declined ${command}`);
				}
			}

			const result = await pi.exec("linctl", [...args, ...FORCED_FLAGS], { signal, timeout: TIMEOUT_MS });
			if (result.killed) {
				throw new Error(`linear: ${command} was cancelled or exceeded ${TIMEOUT_MS / 1000}s`);
			}
			if (result.code !== 0) {
				const detail = result.stderr.trim() || result.stdout.trim() || `exit ${result.code}`;
				throw new Error(`linear: ${command} failed: ${detail}`);
			}

			const truncation = truncateHead(result.stdout, { maxLines: DEFAULT_MAX_LINES, maxBytes: DEFAULT_MAX_BYTES });
			let text = truncation.content;
			if (truncation.truncated) {
				text +=
					`\n\n[Output truncated: ${truncation.outputLines} of ${truncation.totalLines} lines ` +
					`(${formatSize(truncation.outputBytes)} of ${formatSize(truncation.totalBytes)}). ` +
					"Narrow the query with filters or --limit.]";
			}
			return {
				content: [{ type: "text", text }],
				details: { args, destructive: isDestructive(args), truncated: truncation.truncated },
			};
		},

		renderCall(args, theme) {
			const argv = Array.isArray(args.args) ? args.args.join(" ") : "";
			return new Text(theme.fg("toolTitle", theme.bold("linear ")) + theme.fg("accent", argv), 0, 0);
		},
	});
}
