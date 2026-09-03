Adversarial review of the current branch (feature/omniplex-bdc60144) against base main.
The change is committed — inspect it with 'git diff main...HEAD'.

Intent, as the user stated it: in the chat composer, the reasoning-effort and
model dropdowns sat too close to the submit button; they should be consolidated
into a single dropdown whose trigger label shows the model and the effort level
next to each other, with effort as a menu item that opens a submenu of levels;
the effort levels should be formatted properly rather than shown as raw
lowercase ids; there should be a badge indicating which level is the default;
and there should be spacing between that control and the submit button.

Two jobs: (1) does the diff actually do what was asked, or does it miss or
misread requirements; (2) find real defects — logic errors, broken edge cases,
race conditions, type unsoundness, security holes, dead or unreachable paths,
React state bugs, and in particular anything about nested Radix popovers /
dialogs (a Popover rendered inside another Popover's content, and a Sheet inside
a Sheet) that would misbehave in a real browser even though jsdom tests pass.

Do NOT comment on style, naming, file organisation, or anything subjective.
For each finding give file:line, the concrete failure scenario, and why it is
wrong. If you find nothing real, say so.
