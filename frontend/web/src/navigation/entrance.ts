import type { Router } from 'vue-router'
import { LOGIN_PATH } from '../session/session'

// Only an initial login arrival gets a content entrance. Later visits can
// morph from the console, and an entrance would hide the moving content.
const eligible = new WeakMap<Router, boolean>()

export function installEntrance(router: Router) {
  router.afterEach((to, from, failure) => {
    if (!failure)
      eligible.set(router, from.matched.length === 0 && to.path === LOGIN_PATH)
  })
}

export function takeEntrance(router: Router): boolean {
  const entering = eligible.get(router) ?? false
  eligible.delete(router)
  return entering
}
