# Photo Sharing

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

Add `&accent=cyan`, `&accent=sky`, or `&accent=blue` to compare Tailwind accent families. The floating switcher controls both the structural and color variants. The prototype defaults to dark mode and includes a light-mode toggle. All data and interactions are in memory.
