<script setup lang="ts">
import { computed, ref, useTemplateRef, watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { BRAND } from './brand'
import AppIcon from './components/AppIcon.vue'
import BrandMark from './components/BrandMark.vue'
import { useFrame } from './navigation/frame'
import { DEV_ADMIN, isWorkEmail, nextPath, signIn } from './session/session'
import { UiButton, UiCard, UiField, UiInput, useMotionFeedback } from './ui'

const { t } = useI18n({ useScope: 'global' })
const route = useRoute()
const router = useRouter()
const { play } = useMotionFeedback()
const frame = useFrame()
frame.sidebar.value = 'login'
// Vite replaces the flag at build time, so a production bundle drops the
// shortcut.
const dev = import.meta.env.DEV

watchEffect(() => {
  document.title = t('view.login.documentTitle', { brand: BRAND })
})

// The parts of the console the page describes, in the order of its menu.
const AREAS = ['devices', 'clients', 'sites', 'topology'] as const

// One line per visit, picked at random. The key is kept, not the text, so
// the line follows a locale switch.
const LINES = ['amber', 'dns', 'fine', 'layerEight', 'story', 'uptime'] as const
const line = LINES[Math.floor(Math.random() * LINES.length)] ?? LINES[0]

const email = ref('')
// The failed check as a state, not as text, so the message follows a locale
// switch.
const problem = ref<'required' | 'invalid' | ''>('')
const error = computed(() =>
  problem.value === 'required'
    ? t('view.login.emailRequired')
    : problem.value === 'invalid'
      ? t('view.login.emailInvalid')
      : undefined,
)
const field = useTemplateRef<HTMLElement>('field')
const sendable = computed(() => isWorkEmail(email.value.trim()))

async function enterConsole(value: string) {
  signIn(value)
  await router.replace(nextPath(route.query.next))
}

async function submit() {
  const value = email.value.trim()
  problem.value = !value ? 'required' : isWorkEmail(value) ? '' : 'invalid'
  if (problem.value) {
    play(field.value ?? undefined, { x: [-8, 0] }, 0.28)
    return
  }
  await enterConsole(value)
}
</script>

<template>
  <Teleport defer to="#frame-sidebar">
    <div class="product-brand" translate="no">
      <BrandMark />
      <span>{{ BRAND }}</span>
    </div>

    <form
      class="login-form flex flex-col gap-5 my-auto px-3 max-[800px]:mt-6 max-[800px]:px-0"
      novalidate
      @submit.prevent="submit"
    >
      <div>
        <h1 class="m-0 text-2xl font-semibold tracking-tight">
          {{ t('view.login.title', { brand: BRAND }) }}
        </h1>
        <p class="mt-1.5 mb-0 text-md text-muted-foreground">
          {{ t('view.login.lead') }}
        </p>
      </div>
      <!-- The height holds a line for the error, so nothing below moves
             when one appears. -->
      <div class="min-h-[88px]">
        <div ref="field">
          <UiField :label="t('view.login.email')" :error="error">
            <div class="relative">
              <UiInput
                v-model="email"
                class="login-email !h-11 !pr-12 !text-md"
                type="email"
                name="email"
                autocomplete="email"
                inputmode="email"
                :placeholder="t('view.login.emailPlaceholder')"
              />
              <!-- The action appears once the address can be sent. Enter
                     still submits without it and reports what is wrong. -->
              <Transition name="login-send">
                <UiButton
                  v-if="sendable"
                  variant="primary"
                  size="icon"
                  type="submit"
                  class="login-submit absolute top-1.5 right-1.5 cursor-pointer"
                  :aria-label="t('view.login.submit')"
                  :title="t('view.login.submit')"
                >
                  <AppIcon name="arrow" />
                </UiButton>
              </Transition>
            </div>
          </UiField>
        </div>
      </div>
      <UiButton
        v-if="dev"
        variant="ghost"
        size="sm"
        class="login-dev-skip self-start cursor-pointer"
        @click="enterConsole(DEV_ADMIN)"
      >
        {{ t('view.login.devSkip') }}
      </UiButton>
    </form>
  </Teleport>
  <Teleport defer to="#frame-page">
    <main
      class="login-page flex-1 min-h-0 overflow-auto px-[34px] pt-[31px] pb-10 max-[1150px]:px-6 max-[800px]:px-5"
    >
      <div class="max-w-[880px]">
        <h2 class="m-0 text-2xl font-bold tracking-tight text-balance">
          {{ t('view.login.aboutTitle') }}
        </h2>
        <p
          class="mt-2 mb-0 max-w-[62ch] text-md text-muted-foreground text-pretty"
        >
          {{ t('view.login.aboutLead', { brand: BRAND }) }}
        </p>
        <ul
          class="grid grid-cols-2 max-[1150px]:grid-cols-1 gap-4 m-0 mt-7 p-0 list-none"
        >
          <li v-for="area in AREAS" :key="area">
            <UiCard class="login-area h-full bg-card/60">
              <div class="flex items-center gap-2.5 text-md font-semibold">
                <span class="inline-flex text-accent-foreground"
                  ><AppIcon :name="area"
                /></span>
                {{ t(`view.common.pages.${area}`) }}
              </div>
              <p class="mt-1.5 mb-0 text-base text-muted-foreground">
                {{ t(`view.login.about.${area}`) }}
              </p>
            </UiCard>
          </li>
        </ul>
        <p class="login-line mt-10 mb-0 text-base text-muted-foreground">
          {{ t(`view.login.lines.${line}`) }}
        </p>
      </div>
    </main>
  </Teleport>
</template>

<style scoped>
/* The button's own transition covers colours only, so a press and a hover
   would snap. */
.login-submit {
  transition:
    filter 120ms var(--ease-out),
    scale 120ms var(--ease-out),
    opacity 120ms var(--ease-out);
}

.login-submit:active:not(:disabled) {
  scale: 0.94;
}

.login-send-enter-from,
.login-send-leave-to {
  opacity: 0;
  scale: 0.6;
}

@media (prefers-reduced-motion: reduce) {
  .login-submit {
    transition: none;
  }
}
</style>
