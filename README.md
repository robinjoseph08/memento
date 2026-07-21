# Photo Sharing

## Curator workflow prototype

This branch contains a throwaway UI prototype for the Curator publishing workflow. It presents three structurally different variants on one route.

```sh
npm install
npm run prototype
```

Open the local URL with one of these query parameters:

- `?variant=A`: guided work queue
- `?variant=B`: split-pane command center
- `?variant=C`: Event canvas

Use the floating switcher or the left and right arrow keys to move between variants. The prototype defaults to dark mode and includes a light-mode toggle. All data and interactions are in memory.
