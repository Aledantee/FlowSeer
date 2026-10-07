import { nextTick } from 'vue'
import type { Router } from 'vue-router'
import { LOGIN_PATH } from '../session/session'

// Morphs between the login page and the console with the View Transitions
// API: the browser pictures the old page, lets the route change render, and
// animates between the two, moving the elements that share a
// `view-transition-name` (`style.css`). Navigation inside the console is
// untouched. A browser without the API, or a visitor who asked for less
// motion, gets the plain route change.
export function installPageMorph(router: Router) {
  let rendered: (() => void) | undefined

  router.beforeResolve((to, from) => {
    if (
      from.matched.length === 0 ||
      (to.path === LOGIN_PATH) === (from.path === LOGIN_PATH) ||
      typeof document.startViewTransition !== 'function' ||
      window.matchMedia('(prefers-reduced-motion: reduce)').matches
    )
      return
    return new Promise<void>((pictured) => {
      const transition = document.startViewTransition(
        () =>
          new Promise<void>((done) => {
            rendered = done
            pictured()
          }),
      )
      // A transition the browser skips, for example in a hidden tab, rejects
      // these. The route change itself has already happened.
      transition.ready.catch(() => {})
      transition.finished.catch(() => {})
    })
  })

  // Runs for a failed navigation as well, so the old picture never stays up.
  router.afterEach(() => {
    const done = rendered
    rendered = undefined
    if (done) void nextTick(done)
  })
}
