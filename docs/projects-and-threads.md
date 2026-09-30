# Projects, folders and threads

## Why

Omniplex assumes the person using it is a developer with a git checkout. Adding
a project means typing an absolute server path into a text box. Starting a
session means choosing between "Main checkout", "New worktree from issue or
branch name" and "Attach to existing worktree". Someone who wants an agent to
work on a client's files has to learn git words before they can start, and a
plain folder with no git fails the moment they pick the worktree option.

The model is also too small for how work is actually organised. A client like
Bowerbird has three repos and a folder of prototypes. Some threads need one
repo. Some need all four at once. Today a project is exactly one directory, so
the second case has no home.

## The model

```
Project            a record in Omniplex's database
  home folder      optional; where Omniplex puts new things for this project
  folders          what the project points at, anywhere on disk
    folder         a plain folder, or a git repo (badged "Git")
  threads          conversations with an agent, each with a scope
```

A project is a database record that points at folders. Folders can live
anywhere. Each one is either plain or git, and Omniplex works that out by
looking, never by asking.

Projects do not nest. Project, then folders, is as deep as it goes. The
nesting that matters is a thread's scope, below.

### The home folder

When Omniplex creates something for a project (a new folder, a clone), it puts
it in the project's home folder, `<projects folder>/<project-slug>/`.

- **The home folder is never a git repo.** A clone always goes into a folder of
  its own inside the home, even when it is the project's only folder.
  Otherwise the second repo would have to be cloned inside the first, and the
  first repo's git would see it as untracked files.
- **A plain-folder project works in its home folder directly.** "Recipe site"
  is `~/Omniplex/recipe-site/`. A repo added later lands at
  `recipe-site/<repo>/`. A plain folder holding a repo is harmless.
- **Folders you point at stay where they are.** A project made from
  `~/code/omniplex` has no home folder until the first time Omniplex needs to
  create something in it. Nothing is ever moved.
- **A folder inside a git folder of the same project is refused**, with a
  message naming the git folder. That is the nesting that breaks.

With one folder, the UI hides the folder level entirely: the project looks like
that folder. The folder list appears from the second folder on.

### Projects folder

A server setting, `projectsDir` in `~/.omniplex/config.json`, default
`~/Omniplex`, editable in Settings. This replaces the `-cwd` flag, which is
deleted. On a phone, "where" means the machine running Omniplex, so a default
that needs no typing matters more than a good folder picker.

## Creating a project

New project asks for a name. "Bowerbird" creates `~/Omniplex/bowerbird/`,
registers the project with the home folder as its one folder, and opens it. A
taken folder name gets a `-2`, not an error.

Two shortcuts under the name field:

- **From GitHub**: paste a URL or pick from `gh repo list`. Clones into
  `<home>/<repo>/`. The project's one folder is the repo.
- **From a folder on this computer**: a folder browser over `/api/fs`. A plain
  folder becomes the home folder. A git folder is pointed at, and the home
  folder waits until it is needed.

New project is reachable from the sidebar, not only from inside the new-thread
flow.

## Adding to a project

**Add to project** offers:

- **Copy from GitHub**: clone into the home folder.
- **Use a folder on this computer**: point at it where it is.
- **New folder**: an empty folder in the home folder.

Removing a folder from a project only removes the pointer. Files on disk are
never deleted, and the same goes for removing a project.

## Threads

Sessions are renamed threads, everywhere: UI, protocol, Go packages and the
database.

### Scope

Every thread has a scope, shown as a chip on the composer.

- **Everything**: the agent starts in the home folder (created now if the
  project has none) and every folder outside it is passed as an extra folder.
  Claude takes them as `additionalDirectories`. Codex takes them as
  `writableRoots` on its workspace-write sandbox. Pi is still to be checked.
- **One folder**: the agent starts in that folder.

With one folder the scope chip is not shown.

### Git options

Only when the scope is a single git folder:

- **Work in the folder**: today's main checkout.
- **Work on a copy**: today's worktree. Branch name optional, behind a
  disclosure. Attaching to an existing copy stays, one level further in.

A thread scoped to a plain folder, or to Everything, asks nothing. Everything
threads work directly in their folders. Copies across several repos come
later.

### Starting a thread

No dialog. New thread opens an empty thread with the composer focused. Project,
scope, model and permissions are chips above the composer, filled from what was
last used in that project. The thread is created on the server by the first
send, which carries the prompt with it, so starting a thread costs one round
trip and an abandoned draft costs none.

### Permissions

Three levels, named for what they allow, each mapped onto the harness's own
modes. Proposed mapping, to be checked against each mode's real behaviour:

| Level | Claude | Codex |
|---|---|---|
| Ask before changing anything | Manual | Manual |
| Edit files, ask before commands | Accept edits | Ask when needed |
| Do everything | Bypass | Bypass |

The harness's own modes, Plan included, stay under Advanced.

## Where settings live

Everything moves into the database. `.omniplex/project.json` is no longer read
or written.

- **Project**: name, home folder, default model and permissions per harness.
- **Folder**: path, base branch, where copies go (default `.worktrees`),
  provision and deprovision hook paths and timeouts. The hook scripts stay in
  the repo. Only the pointers to them move.

On first start after this ships, each existing `project.json` is read once
into the database. The files are left on disk and ignored from then on.

`omniplex relocate` already rewrites database paths. It now rewrites folder
paths instead of project roots.

## Settings screen

New, reached from the sidebar footer:

- Projects folder
- Default model and permission level for new projects
- Branch name format (moved out of project settings, where it already saved
  per machine)

Providers and Access stay where they are.

## Migrating what exists

Each existing project becomes a project with one folder, its old root, which is
git. It has no home folder. Each session becomes a thread scoped to that
folder, with its workspace mode carried over. Nothing looks different for a
single-repo project.

## Not in this work

- Agents, including scheduled ones and the Eve agents from Harvest. Later
  phases.
- Copies across several repos in one thread.
- Change tracking for plain folders. A plain folder has no changes card and no
  diff.

## Order

1. Rename sessions to threads throughout. A pure rename, reviewed on its own.
2. Database model for projects and folders, the `project.json` import, the
   projects folder setting, and deleting `-cwd`.
3. New project and Add to project, with the folder browser and GitHub clone.
4. Thread scope, extra folders per harness, git options only for git folders.
5. Composer chips replacing the new-session dialog, and first-send creation.
6. Permission levels and the Settings screen.

## Assumed, not yet confirmed

- The rename goes through code, protocol and database, not only UI text.
- The default projects folder is `~/Omniplex`.
- New threads start without a dialog.
- The permission level mapping above.
- This work stacks on PR #188 (artefacts), which already made artefacts files
  in the project's home folder.
