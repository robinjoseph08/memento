# Memento

## Curator workflow prototype

This branch contains a throwaway UI prototype for Memento's Curator publishing workflow. It presents three structurally different variants on one route, defaulting to the selected split-pane command center.

```sh
npm install
npm run prototype
```

Open the local URL with one of these query parameters:

- `?variant=A`: guided work queue
- `?variant=B`: split-pane command center
- `?variant=C`: Event canvas

Tailwind sky is the selected accent family. Add `&accent=cyan`, `&accent=sky`, or `&accent=blue` to revisit the retained color comparisons. The floating switcher controls both the structural and color variants. The prototype defaults to dark mode and includes a light-mode toggle. All data and interactions are in memory.
