<script setup lang="ts">
import { computed, ref, useTemplateRef, watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { BRAND } from './brand'
import AppIcon from './components/AppIcon.vue'
import BrandMark from './components/BrandMark.vue'
import TrafficSparkline from './components/TrafficSparkline.vue'
import { useFormat } from './i18n/format'
import { useFrame } from './navigation/frame'
import { DEV_ADMIN, isWorkEmail, nextPath, signIn } from './session/session'
import { UiButton, UiField, UiInput, useMotionFeedback } from './ui'

const { t } = useI18n({ useScope: 'global' })
const fmt = useFormat()
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

const PATH = ['gateway', 'switch', 'accessPoint'] as const
const PATH_ICONS = {
  gateway: 'gateway',
  switch: 'switch',
  accessPoint: 'access-point',
}
const TRAFFIC = [42, 48, 44, 62, 58, 74, 66, 82, 70, 88, 80, 84]

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
      class="login-page flex-1 min-h-0 overflow-auto"
      tabindex="0"
      aria-labelledby="login-overview-title"
    >
      <div class="login-overview">
        <header class="login-intro">
          <p class="login-eyebrow text-accent-foreground">
            <AppIcon name="topology" />
            {{ t('view.login.overview.eyebrow') }}
          </p>
          <h2
            id="login-overview-title"
            class="m-0 text-3xl font-semibold tracking-tight text-balance"
          >
            {{ t('view.login.overview.title') }}
          </h2>
          <p
            class="mt-4 mb-0 max-w-[56ch] text-lg text-muted-foreground text-pretty"
          >
            {{ t('view.login.overview.lead') }}
          </p>
        </header>

        <figure class="login-network m-0">
          <figcaption class="login-network-caption">
            <span class="inline-flex items-center gap-2 font-semibold">
              <AppIcon name="sites" />
              {{ t('view.login.overview.site') }}
            </span>
            <span class="text-muted-foreground">{{
              t('view.login.overview.example')
            }}</span>
          </figcaption>
          <ol class="login-path">
            <li v-for="node in PATH" :key="node" class="login-node">
              <div class="login-node-icon" aria-hidden="true">
                <AppIcon :name="PATH_ICONS[node]" />
              </div>
              <div class="login-node-copy">
                <h3 class="m-0 text-lg font-semibold">
                  {{ t(`view.login.overview.path.${node}.title`) }}
                </h3>
                <p class="mt-1 mb-0 text-md text-muted-foreground">
                  {{ t(`view.login.overview.path.${node}.detail`) }}
                </p>
              </div>
            </li>
          </ol>
          <div class="login-traffic">
            <div>
              <p class="m-0 text-md font-semibold">
                {{ t('view.login.overview.trafficTitle') }}
              </p>
              <p class="mt-1 mb-0 text-md text-muted-foreground">
                {{ t('view.login.overview.trafficPeriod') }}
              </p>
            </div>
            <TrafficSparkline
              class="login-traffic-chart"
              :values="TRAFFIC"
              :label="
                t('view.login.overview.trafficSummary', {
                  start: fmt.rate(42),
                  end: fmt.rate(84),
                })
              "
            />
            <p class="login-traffic-value m-0 text-xl font-semibold">
              {{ fmt.rate(84) }}
            </p>
          </div>
        </figure>

        <div class="login-notes">
          <section v-for="topic in ['trace', 'inspect'] as const" :key="topic">
            <h3 class="m-0 text-lg font-semibold">
              {{ t(`view.login.overview.${topic}.title`) }}
            </h3>
            <p class="mt-2 mb-0 text-md text-muted-foreground text-pretty">
              {{ t(`view.login.overview.${topic}.body`) }}
            </p>
          </section>
        </div>
      </div>
    </main>
  </Teleport>
</template>

<style scoped>
.login-page {
  container-type: inline-size;
}

.login-page:focus-visible {
  outline: 2px solid var(--ring);
  outline-offset: -4px;
}

.login-overview {
  max-width: 1000px;
  margin: auto;
  padding: clamp(24px, 5cqi, 72px);
}

.login-intro {
  max-width: 640px;
}

.login-eyebrow {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 24px;
  font-size: var(--text-md);
  font-weight: 600;
}

.login-network {
  margin-top: 40px;
  overflow: hidden;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: var(--radius-panel);
  box-shadow: var(--shadow-sm);
}

.login-network-caption {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px 16px;
  padding: 16px 24px;
  border-bottom: 1px solid var(--border);
  font-size: var(--text-md);
}

.login-path {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  margin: 0;
  padding: 40px 24px;
  list-style: none;
}

.login-node {
  position: relative;
  min-width: 0;
  text-align: center;
}

.login-node:not(:last-child)::after {
  position: absolute;
  top: 31px;
  right: calc(-50% + 40px);
  left: calc(50% + 40px);
  height: 2px;
  background: var(--graph-edge);
  content: '';
}

.login-node-icon {
  display: grid;
  width: 64px;
  height: 64px;
  margin: 0 auto 16px;
  place-items: center;
  color: var(--accent-foreground);
  background: var(--subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-panel);
}

.login-node-icon svg {
  width: 28px;
  height: 28px;
}

.login-node-copy {
  padding-inline: 8px;
}

.login-traffic {
  display: grid;
  grid-template-columns: 1fr minmax(100px, 1fr) auto;
  align-items: center;
  gap: 24px;
  padding: 24px;
  border-top: 1px solid var(--border);
}

.login-traffic-value {
  white-space: nowrap;
}

.login-notes {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 32px;
  margin-top: 32px;
}

@container (width < 600px) {
  .login-path {
    grid-template-columns: 1fr;
    gap: 32px;
    padding: 24px;
  }

  .login-node {
    display: flex;
    align-items: center;
    gap: 16px;
    text-align: start;
  }

  .login-node-icon {
    flex-shrink: 0;
    margin: 0;
  }

  .login-node-copy {
    padding: 0;
  }

  .login-node:not(:last-child)::after {
    top: 72px;
    right: auto;
    left: 31px;
    width: 2px;
    height: 16px;
  }

  .login-traffic {
    grid-template-columns: 1fr auto;
    gap: 16px;
  }

  .login-traffic-chart {
    grid-row: 2;
    grid-column: 1 / -1;
  }

  .login-notes {
    grid-template-columns: 1fr;
    gap: 24px;
  }
}

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
