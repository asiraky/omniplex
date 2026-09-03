Found 1 real defect. The primary overflow fix is otherwise correct.

- **[Sidebar.tsx:350](/Users/aaron/code/omniplex/.worktrees/feature-omniplex-d0f99a0b/web/src/components/Sidebar.tsx:350)** — `max-w-[50%] shrink-0` always caps the project label at half the metadata row. For sessions with no branch—common during provisioning/failure—or a short branch, long project names ellipsize despite substantial unused space before the provider badge. This affects both mobile and wider docked sidebars and is wrong because content truncates when it would fit.

The `min-w-0` addition at line 305 correctly prevents the grid item from expanding past the panel and does not disrupt the `1fr → 0fr` delete animation. No other concrete regressions were found in hover, active, busy/failed, mobile, exit-animation, or RTL states.

The repository has no `staging` ref, so I reviewed `b4cc142...HEAD`; `b4cc142` is HEAD’s parent and current `main`. TypeScript checking passed. UI tests could not start because the read-only sandbox prevented Vite from writing its temporary config file.