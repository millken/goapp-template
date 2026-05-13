// Shared protocol constants between Go server (github.com/millken/inertia)
// and the JS client. Keep these in sync with the Go side; do not rename
// without coordinating the server.

/** Page-data key whose value selects which Vue component to render. */
export const INERTIA_VIEW_KEY = '_ViEW_'

/** Placeholder rendered into the SSR HTML when no page data is present. */
export const INERTIA_DATA_PLACEHOLDER = '<!--inertia-data-page-inertia-->'
