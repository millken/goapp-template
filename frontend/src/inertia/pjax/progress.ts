// The top progress bar.
//
// Hand-rolled rather than pulled from a package: it is one element, one
// transition and a timer, and a template should not add a dependency for that.

/**
 * How long a navigation may take before it earns a progress bar. Anything
 * faster feels instant, and showing a bar for 40ms reads as a flicker.
 */
export const PROGRESS_DELAY_MS = 100

const STYLE_ID = 'pjax-progress-style'

const CSS = `
[data-pjax-progress] {
  position: fixed;
  top: 0;
  left: 0;
  height: 2px;
  width: 100%;
  transform: scaleX(0);
  transform-origin: 0 50%;
  background: currentColor;
  color: #2563eb;
  opacity: 0.9;
  z-index: 2147483647;
  pointer-events: none;
  animation: pjax-progress-grow 10s cubic-bezier(0.1, 0.7, 0.3, 1) forwards;
}
@keyframes pjax-progress-grow {
  0%   { transform: scaleX(0); }
  40%  { transform: scaleX(0.6); }
  100% { transform: scaleX(0.95); }
}
@media (prefers-reduced-motion: reduce) {
  [data-pjax-progress] { animation: none; transform: scaleX(0.6); }
}
`

export interface Progress {
  start(): void
  done(): void
}

export function createProgress(doc: Document): Progress {
  let timer: ReturnType<typeof setTimeout> | null = null
  let bar: HTMLElement | null = null

  function ensureStyle(): void {
    if (doc.getElementById(STYLE_ID)) return
    const style = doc.createElement('style')
    style.id = STYLE_ID
    style.textContent = CSS
    doc.head.append(style)
  }

  function show(): void {
    if (bar) return
    ensureStyle()
    bar = doc.createElement('div')
    bar.setAttribute('data-pjax-progress', '')
    // Decorative: the navigation itself is what a screen reader should notice.
    bar.setAttribute('aria-hidden', 'true')
    doc.body.append(bar)
  }

  return {
    start() {
      // An overlapping navigation keeps the bar it already has; restarting the
      // timer would hide feedback the user is currently looking at.
      if (timer || bar) return
      timer = setTimeout(() => {
        timer = null
        show()
      }, PROGRESS_DELAY_MS)
    },
    done() {
      if (timer) {
        clearTimeout(timer)
        timer = null
      }
      bar?.remove()
      bar = null
    },
  }
}
