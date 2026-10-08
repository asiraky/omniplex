---
name: omniplex
description: Adds or removes MCP servers, skills and CLI sign-ins through Omniplex. Use when the user asks to add, install, connect, remove or uninstall an MCP server or a skill, or to set up a sign-in or account for a command-line tool.
---

# Managing MCP servers, skills and sign-ins in Omniplex

Omniplex owns these. Make every change through the omniplex tools (`mcp__omniplex__*`), never by editing `.mcp.json`, `config.toml` or a skills folder yourself: Omniplex keeps secrets out of plaintext, reaches every harness, and links skills where each harness looks.

If the omniplex tools are not available to you (Pi has none), tell the user to make the change on Omniplex's MCP or Skills page, and stop there.

## Every change is a card

Each write tool (`add_mcp_server`, `remove_mcp_server`, `install_skill`, `create_skill`, `remove_skill`, `add_sign_in`, `add_account`) puts a card in the conversation. Nothing changes until the user taps it, in every permission mode, bypass included. The user can edit the proposal on the card first. The call waits for that tap and returns what the user did: saved, edited and saved (with the edits), or declined. Report it as it came back.

A call can time out while the card sits on the user's phone. The card still works. Check with `list_mcp_servers`, `list_skills` or `list_sign_ins` before your next step, and propose again only if the card was declined.

## Secrets

The card has fields for header values, env values and keys. Leave them for the user to type there. If the user already gave you a value in the conversation, pass it in the config; the card shows it masked and it is saved only to the secret store. Asking the user to paste a key into the chat is the wrong move: point them at the card.

## Where it goes

- In a project, servers and skills default to the project. Pass `scope: "everywhere"` or `destination: "personal"` only when the user asks for everywhere: those reach every thread in every project.
- Skill destinations: `project` is the project's own, private and never committed; `repo` commits it into the repository for everyone who clones it; `personal` is everywhere.
- A thread outside a project can only propose everywhere.

## Sign-ins

A sign-in is a CLI definition: how to check status, how to sign in, and the env that selects an account. Work these out from the tool's `--help` and pass them to `add_sign_in`. `add_account` adds a named account to a CLI that has one. Either way the user signs in from the card with a tap. `list_sign_ins` says how to use each account (its env, such as `GOOGLE_WORKSPACE_CLI_CONFIG_DIR=…`); set that env on the commands you run.

## When you can use it

The tool result says when; promise only that.

- MCP server on Claude: added to this session once saved.
- MCP server on Codex: the next session.
- Skill on Claude: this session when its skills folder existed at start, else the next session.
- Skill on Codex: the next turn.
- Skill on Pi: the next session.
