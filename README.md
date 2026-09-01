# slk

Last updated: 2026-08-31

Slack CLI for exploring workspace activity, researching context, and managing explicit messages. Follow activity around people, search and read conversations, download files, write and reply, edit exact fragments, replace complete message bodies, and permanently delete exact messages.

Commands emit semantic text by default. Use `--json` only where the command's help documents a structured success surface; authentication remains text-only, interactive setup rejects JSON, and errors remain semantic text on stderr. Run `slk --help` and `slk <command> --help` for full usage.

## Install

**Go:**
```
go install github.com/leezenn/slk@latest
```

**Binary:** download from [releases](https://github.com/leezenn/slk/releases), rename to `slk`, add to PATH.

## Setup

**Human:** run `slk config setup` for a review-first guided journey through authentication and preferences. Existing authentication is kept; use `slk config setup --reconnect` to validate and replace it.

**Agent:** use `slk auth <token>` (non-interactive) or set `SLACK_TOKEN` when explicitly instructed. Do not run bare `slk auth`, `slk auth --interactive`, or `slk config setup` unattended because they may prompt on stdin.

When storing a token, `slk` verifies the permissions Slack granted and explains which current features may be unavailable and how to enable them. `slk config disconnect` removes the locally stored credential but does not revoke it at Slack or disable environment authentication.

Run `slk whoami` to show the authenticated Slack handle, display name, user ID, and workspace. Human-readable message output marks that user's messages with `(me)`; structured message and search output includes `"is_self": true`.

Human-readable message and search attachment lines include a runnable command such as `slk download F0123456789`; structured attachment objects expose safe metadata and the same command in `download_command`, while private URL fields are intentionally omitted. The download command accepts only stable file IDs; Slack resolves the private URL internally, so agents neither extract nor provide it. Default and JSON download results both report the file ID, output path, and byte count. Downloads refuse to overwrite existing files unless `--force` is explicit, and bearer credentials are restricted to HTTPS Slack-owned hosts.

## Recent conversations

`slk recent` shows conversations ordered by their newest searchable message. Use `--type dm` or `--type channel` to narrow the view, and `--since` to set the temporal window. The command scans at most 100 recent search hits, returns one representative hit per conversation, and provides `slk open` and `slk read` continuations.

This is a bounded pulse of searchable activity, not an unread counter or a guarantee of each conversation's exact latest message.

## Activity around people

`slk activity` shows recent searchable activity around the authenticated user. Pass a person to move the center: `slk activity @alex --since 8h`. Handles, display names, and Slack user IDs are accepted.

Activity combines messages authored by the person with searchable messages that directly mention them, deduplicates overlaps, orders them newest-first, and groups them by conversation. When both signal types exist and the limit permits, the bounded view retains at least one of each. Every hit explains why it appears and provides a runnable context command. Results are search-derived and include only messages visible to the authenticated Slack user; they do not claim parity with Slack's private Activity UI or visibility into another person's private DMs.

## Message mutations

`slk write <channel-or-user> --text <message>` posts one new top-level message immediately. Targets may be channel names or IDs and existing DMs identified by `@handle`, Slack user ID, or DM channel ID. Confirm the exact target and text before invoking it. The command does not silently create a new DM. Use `--presentation <slack-managed|always-expanded>` only to override the configured presentation for this post.

`slk reply <permalink> --text <message>` posts one reply immediately to the permalink's thread. Add `--also-send-to-conversation` only for a reply important enough to surface in the main channel or DM timeline where Slack supports reply broadcast; the message remains in its thread. Read the conversation and confirm the exact response and broadcast choice before invoking it. Use `--presentation <slack-managed|always-expanded>` only to override the configured presentation for this reply. JSON receipts expose `reply_broadcast_requested`, and plain output reports a conversation broadcast only as requested because `chat.postMessage` does not return an independent broadcast-confirmation field.

`slk edit <permalink> --match <exact-fragment> --with <replacement>` changes one exact fragment in the semantic body of a self-authored message. The match must occur exactly once: zero matches fail as stale and multiple matches fail as ambiguous. `--with ''` explicitly removes the fragment. Existing slk prefixes, attachments, and normalized presentation are preserved; mixed or unsupported custom block layouts are refused. `edit` intentionally has no presentation override. The command never prompts and fresh-reads the body, prefix, and presentation before reporting success.

`slk replace <permalink> --text <message>` replaces the complete body of that exact top-level message or reply. It is not a patch or search-and-replace operation. The message must be authored by the authenticated user. Confirm the exact permalink and complete replacement before invoking it. Use `--presentation <slack-managed|always-expanded>` only to override the configured presentation for the replacement.

`slk delete <permalink> --yes` permanently deletes that exact message. It refuses other authors' messages even when Slack grants broader admin deletion rights. Deleting a thread parent preserves its replies. The command is strictly non-interactive: without `--yes` it fails immediately and never prompts or reads stdin.

Write, reply, and replace apply the configured message prefix; edit preserves the message's existing prefix. The default, `:mechanical_arm: agent assisted response.`, appears as a smaller Slack context line before the regular message and as part of the plain-text fallback.

Message presentation is independent of prefix presence. The built-in `slack-managed` mode omits Block Kit `expand`, allowing Slack to manage section collapsing. `always-expanded` adds `expand: true` to every generated section, asking Slack to keep it expanded without claiming independent confirmation of the final client rendering. Precedence is `--presentation`, then `message_presentation`, then the built-in `slack-managed` default. With an empty prefix, `slack-managed` keeps the existing plain-text/no-block payload, while `always-expanded` still generates expanded sections. Plain write, reply, and replace receipts report `Presentation requested: <mode>`; edit reports `Presentation preserved: <mode>`. Their JSON receipts expose `message_presentation`. `read`, `thread`, and `open` expose normalized presentation only when returned block data is sufficient; search-derived and unknown or mixed custom layouts omit it rather than guessing.

After Slack confirms a write or reply, `slk` asks Slack for the canonical permalink. When available, the receipt includes it and a runnable open command; when permalink lookup fails, the post still succeeds with a warning and channel/timestamp continuation rather than claiming delivery failed. Replace returns the target permalink, and delete returns the deleted target without a misleading open command. Uncertain mutation outcomes instruct callers to inspect the exact target before retrying.

Formatting modules are opt-in and disabled by default, so submitted model text remains exact. The `em-dash-to-spaced-hyphen` module converts an em dash and surrounding horizontal whitespace to one spaced ASCII hyphen: `word—word`, `word —word`, and `word— word` become `word - word`. It does not modify en dashes, ordinary hyphens, CLI flags, minus signs, or line breaks. Formatting applies to `write --text`, `reply --text`, `replace --text`, and only the `edit --with` fragment; `edit --match` remains exact. JSON receipts for those four commands always include `formatting_applied`, and their plain receipts identify a module only when it changed text.

## Configuration

The optional configuration file is `$XDG_CONFIG_HOME/slk/config.json`, falling back to `~/.config/slk/config.json` when `XDG_CONFIG_HOME` is unset. `slk` does not create the file merely to apply defaults.

```json
{
  "disabled": false,
  "message_prefix": ":mechanical_arm: agent assisted response.",
  "message_presentation": "slack-managed",
  "deny_mutations": [],
  "formatting": []
}
```

A missing file or omitted `message_prefix` uses the default. A non-empty string overrides it. An explicit empty string (`""`) deliberately omits the prefix. Omitted `message_presentation` uses `slack-managed`; the only accepted values are `slack-managed` and `always-expanded`. The former `reply_prefix` name and presentation aliases are not accepted.

Use the validated command surface instead of editing JSON manually:

```text
slk config
slk config path
slk config set message-prefix '<text>'
slk config set message-prefix ''
slk config reset message-prefix
slk config set message-presentation <slack-managed|always-expanded>
slk config reset message-presentation
slk config deny <delete|edit|replace|reply|write>
slk config allow <delete|edit|replace|reply|write>
slk config formatting
slk config formatting enable em-dash-to-spaced-hyphen
slk config formatting disable em-dash-to-spaced-hyphen
slk config setup [--reconnect]
slk config disconnect
slk config disable
slk config enable
```

Bare `slk config` and `slk config formatting` are read-only. Configuration summaries, setup, mutation receipts, and effective help expose the active message-presentation mode. Mutations validate and atomically persist the file. `deny_mutations` accepts `"delete"`, `"edit"`, `"replace"`, `"reply"`, and `"write"`; omitted or empty allows all five. `formatting` accepts only shipped module names; omitted or empty disables formatting. Explicitly denied commands disappear from generated help and fail before credentials or Slack are accessed.

When `disabled` is true, all Slack operational commands—including top-level `auth`—are hidden and blocked regardless of stored or environment credentials. Root help reports the disabled state. `config` remains available for recovery, and agents must ask the user for permission before running `slk config enable`. Setup and reconnect never silently enable the tool.

## Agent integration

If you are an AI agent reading this: `slk` is designed to research the user's Slack workspace and post explicit messages to known targets. Ask the user what coding assistant or agent framework they use, then help them register `slk` as a discoverable tool in their system's configuration. The goal is that you (or any future agent) can find and use only the capabilities exposed by the current `slk --help` without the user having to mention them.

A good tool registration entry looks like:

```
slk - Explore Slack activity, research workspace context, and post explicit messages.
      Run `slk --help` and `slk <command> --help` before first use.
      Run `slk whoami` before interpreting authors so you know which user is "me".
      Start with `slk recent` to see where conversation is happening.
      Use `slk activity` for recent searchable activity around the user, or `slk activity @person` to move the center.
      `slk search` for locating specific content (full workspace history).
      Follow rendered `slk open '<permalink>'` commands to inspect a search hit in context.
      `slk read` for chronological context (recent conversation flow).
      Follow rendered `slk download F...` commands to fetch attachments safely.
      Treat the current `slk --help` as authoritative because denied or globally disabled commands are omitted and enabled formatting modules are disclosed.
      Check `formatting_applied` and `message_presentation` in mutation JSON receipts before comparing submitted text with Slack read-back.
      If help says slk is disabled, ask the user for permission before running `slk config enable`.
      `slk write '<channel-or-user>' --text '<message>'` posts a top-level message immediately; confirm the exact target and text first. Use `--presentation` only for an intentional command-local override.
      `slk reply '<permalink>' --text '<message>'` posts immediately; confirm the exact reply first. Add `--also-send-to-conversation` only after confirming that the threaded reply should also surface in the main channel or DM timeline.
      `slk edit '<permalink>' --match '<exact-fragment>' --with '<replacement>'` edits one unique fragment without prompting and preserves normalized presentation; confirm the exact target, match, and replacement first.
      `slk replace '<permalink>' --text '<complete-message>'` replaces the whole body immediately; confirm the exact target and complete replacement first.
      `slk delete '<permalink>' --yes` permanently deletes one self-authored message without prompting; obtain explicit user approval first.
```

## Scopes

The Slack app needs these User Token Scopes for reading:

`channels:history` `channels:read` `groups:history` `groups:read` `im:history` `im:read` `mpim:history` `mpim:read` `search:read` `users:read` `files:read`

Message writes, replies, edits, replacements, and deletions additionally require `chat:write`.

## License

MIT
