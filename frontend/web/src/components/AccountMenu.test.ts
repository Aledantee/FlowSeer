// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import { RouterView, createMemoryHistory, createRouter } from 'vue-router'
import AccountMenu from './AccountMenu.vue'
import { createWebI18n } from '../i18n'
import { sessionOperator, signIn, signOut } from '../session/session'
import { UiAppRoot } from '../ui'

let dispose = () => {}

afterEach(() => {
  dispose()
  dispose = () => {}
  signOut()
  localStorage.clear()
  document.body.replaceChildren()
})

describe('AccountMenu', () => {
  it('logs out and returns to the login page', async () => {
    signIn('ada@example.com')
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/login', component: { render: () => h('p', 'login') } },
        { path: '/dashboard', component: AccountMenu },
      ],
    })
    await router.push('/dashboard')
    const host = document.createElement('div')
    document.body.append(host)
    const app = createApp({
      render: () => h(UiAppRoot, {}, () => h(RouterView)),
    })
    app.use(createWebI18n('en')).use(router).mount(host)
    dispose = () => app.unmount()
    await router.isReady()
    await nextTick()

    host.querySelector<HTMLButtonElement>('button.account-trigger')?.click()
    await nextTick()
    await new Promise((resolve) => setTimeout(resolve, 20))
    const logout = document.body.querySelector<HTMLElement>('.account-logout')
    if (!logout) throw new Error('Missing log out item')
    expect(logout.hasAttribute('data-disabled')).toBe(false)
    expect(logout.textContent).toContain('Log out')

    logout.click()
    await new Promise((resolve) => setTimeout(resolve, 20))
    await nextTick()

    expect(sessionOperator.value).toBeNull()
    expect(localStorage.getItem('flowseer.session')).toBeNull()
    expect(router.currentRoute.value.path).toBe('/login')
  })
})
