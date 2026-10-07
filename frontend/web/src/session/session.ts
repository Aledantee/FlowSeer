import { readonly, ref } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import { isPagePath } from '../navigation/page'

const STORAGE_KEY = 'flowseer.session'
export const CONSOLE_HOME = '/dashboard'
export const LOGIN_PATH = '/login'

// Browser storage can be unavailable in restricted browsing contexts, which
// reads as signed out.
function savedOperator(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

// No identity provider is connected, so the session is a preview: the email
// the visitor typed, kept in browser storage. It proves nothing about who
// they are and protects no data, because the console only shows fixtures.
const operator = ref<string | null>(savedOperator())

export const sessionOperator = readonly(operator)

export function isWorkEmail(value: string): boolean {
  // HTML's email grammar, with at least two domain labels for a work address.
  // https://html.spec.whatwg.org/multipage/input.html#email-state-(type=email)
  return /^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+(?![\s\S])/.test(
    value,
  )
}

// A session the browser cannot store still lasts until the page reloads.
export function signIn(email: string) {
  operator.value = email
  try {
    localStorage.setItem(STORAGE_KEY, email)
  } catch {
    // Kept in memory only.
  }
}

export function signOut() {
  operator.value = null
  try {
    localStorage.removeItem(STORAGE_KEY)
  } catch {
    // Nothing was stored.
  }
}

// Where sign-in sends the visitor. Only a console page is accepted, so a
// crafted `next` cannot send them to another site.
export function nextPath(next: unknown): string {
  if (typeof next !== 'string') return CONSOLE_HOME
  const path = next.split(/[?#]/)[0] ?? ''
  return isPagePath(path) ? next : CONSOLE_HOME
}

// The redirect a navigation needs, if any: a signed-out visitor reaches only
// the login page, and a signed-in one skips it.
export function sessionRedirect(to: {
  path: string
  fullPath: string
}): RouteLocationRaw | undefined {
  if (operator.value) return to.path === LOGIN_PATH ? CONSOLE_HOME : undefined
  if (to.path === LOGIN_PATH) return undefined
  return to.fullPath === CONSOLE_HOME
    ? { path: LOGIN_PATH }
    : { path: LOGIN_PATH, query: { next: to.fullPath } }
}
