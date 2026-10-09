package thread

// omniplexInstructions is what every harness is told about the app running
// it, so that when the user mentions Omniplex, or a project, label or
// artefact, the agent knows what they mean. It points at GitHub rather than a
// local checkout, which is somewhere different on every machine.
const omniplexInstructions = `You are running inside Omniplex (https://github.com/asiraky/omniplex), an open-source app that runs Claude Code, Codex and Pi sessions through their SDKs and gives them a web UI. Omniplex started this session and manages it: its projects, threads, worktrees, labels and artefacts, and the MCP servers, skills and sign-ins you have. When the user mentions Omniplex or one of those things, this is what they mean. If you need more detail than that, read the specific files you need from the GitHub repo (gh api or a web fetch). Don't clone it. If the project you're working in is Omniplex itself, read the local checkout instead.`
