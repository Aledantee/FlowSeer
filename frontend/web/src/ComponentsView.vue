<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppIcon from './components/AppIcon.vue'
import UiButton from './components/UiButton.vue'
import StatusBadge from './components/StatusBadge.vue'
import MetricCard from './components/MetricCard.vue'

const catalog = [
  {
    id: 'buttons',
    name: 'Buttons',
    category: 'Actions',
    description: 'One clear next step. Secondary actions stay quiet.',
    guidance:
      'Use one primary action per task. Name the outcome with a verb. Disabled controls retain their label.',
    question: 'Which actions need a loading state before backend integration?',
  },
  {
    id: 'status',
    name: 'Status badges',
    category: 'Feedback',
    description: 'Health that can be understood at a glance.',
    guidance:
      'Always pair color with a written status. Reserve green, amber, and rose for health; cyan communicates selection.',
    question:
      'How should unknown or stale health differ from an offline device?',
  },
  {
    id: 'inputs',
    name: 'Inputs & filters',
    category: 'Forms',
    description: 'Keep the scope and the query easy to understand.',
    guidance:
      'Keep labels visible. Use placeholders for examples, and put validation beside the field it belongs to.',
    question: 'Which filters deserve a persistent place in the device toolbar?',
  },
  {
    id: 'metrics',
    name: 'Metric cards',
    category: 'Data display',
    description: 'A number, its unit, and enough context to interpret it.',
    guidance:
      'Use tabular figures for changing values. Keep units smaller and explain the measurement underneath.',
    question: 'Which metric should open a filtered view when selected?',
  },
  {
    id: 'empty',
    name: 'Empty states',
    category: 'Feedback',
    description: 'Explain what happened and offer a useful next step.',
    guidance:
      'Distinguish an empty fleet from a search with no matches. Preserve scope when clearing a search.',
    question:
      'What should the first-run experience offer before devices exist?',
  },
]
const section = ref('components')
const selectedId = ref('buttons')
const selected = computed(
  () => catalog.find((item) => item.id === selectedId.value) ?? catalog[0],
)
const variant = ref<'primary' | 'secondary' | 'ghost'>('primary')
const disabled = ref(false)
const feedback = ref('')
const search = ref('')
const health = ref('All statuses')
const font = ref('Inter Variable')
const notes = ref('')
const stage = ref('Exploring')
const saved = ref('')
const dirty = ref(false)
const stages = ['Exploring', 'Styling', 'Ready to build', 'Implemented']
const storageKey = computed(
  () => `flowseer.component-draft.${selectedId.value}`,
)
function selectComponent(id: string) {
  selectedId.value = id
  feedback.value = ''
}
function loadDraft() {
  notes.value = ''
  stage.value = 'Exploring'
  saved.value = ''
  try {
    const raw = localStorage.getItem(storageKey.value)
    if (raw) {
      const draft: unknown = JSON.parse(raw)
      if (
        typeof draft === 'object' &&
        draft !== null &&
        'notes' in draft &&
        typeof draft.notes === 'string' &&
        'stage' in draft &&
        typeof draft.stage === 'string' &&
        stages.includes(draft.stage)
      ) {
        notes.value = draft.notes
        stage.value = draft.stage
      } else {
        saved.value =
          'This saved draft could not be read. You can save a new one.'
      }
    }
  } catch {
    saved.value =
      'Draft storage is unavailable or unreadable. Keep a copy of your notes.'
  }
  dirty.value = false
}
watch(selectedId, loadDraft, { immediate: true })
function saveDraft() {
  try {
    localStorage.setItem(
      storageKey.value,
      JSON.stringify({ notes: notes.value, stage: stage.value }),
    )
    saved.value = 'Saved in this browser.'
    dirty.value = false
  } catch {
    saved.value = 'Could not save. Copy your notes before leaving this page.'
  }
}
function editDraft() {
  dirty.value = true
  saved.value = 'Unsaved changes'
}
const swatches = [
  { name: 'Canvas', token: '--page', purpose: 'The quiet background' },
  { name: 'Surface', token: '--surface', purpose: 'Content and data' },
  {
    name: 'Selection',
    token: '--cyan',
    purpose: 'Navigation and focus context',
  },
  { name: 'Action', token: '--coral', purpose: 'The primary next step' },
  { name: 'Healthy', token: '--healthy-surface', purpose: 'Normal operation' },
  {
    name: 'Attention',
    token: '--warning-surface',
    purpose: 'Needs investigation',
  },
]
</script>

<template>
  <div class="component-workspace">
    <div class="page-heading">
      <div>
        <span class="eyebrow">DESIGN WORKSPACE</span>
        <h1>Components</h1>
        <p>A shared language for everything we build.</p>
      </div>
      <span class="workbench-label"
        ><span class="healthy-dot"></span> Living library</span
      >
    </div>
    <nav class="workbench-tabs" aria-label="Component workspace">
      <button
        v-for="tab in ['components', 'foundations']"
        :key="tab"
        :aria-pressed="section === tab"
        @click="section = tab"
      >
        {{ tab === 'components' ? 'Component library' : 'Foundations'
        }}<span v-if="tab === 'components'">{{ catalog.length }}</span>
      </button>
    </nav>

    <div v-if="section === 'components'" class="workbench-layout">
      <aside class="component-index" aria-label="Choose a component">
        <span class="eyebrow">LIBRARY</span>
        <button
          v-for="item in catalog"
          :key="item.id"
          :aria-pressed="selectedId === item.id"
          :disabled="dirty && selectedId !== item.id"
          @click="selectComponent(item.id)"
        >
          <span
            >{{ item.name }}<small>{{ item.category }}</small></span
          ><AppIcon name="arrow" />
        </button>
        <p>Built from the same components used in the console.</p>
        <p v-if="dirty">Save your draft before switching components.</p>
      </aside>
      <div v-if="selected" class="component-detail">
        <header class="component-heading">
          <div>
            <span class="eyebrow">{{ selected.category }}</span>
            <h2>{{ selected.name }}</h2>
            <p>{{ selected.description }}</p>
          </div>
          <span class="draft-tag">{{ stage }}</span>
        </header>
        <section class="specimen-panel" aria-label="Live component preview">
          <div class="specimen-toolbar">
            <span>LIVE PREVIEW</span><span>Uses current theme</span>
          </div>
          <div class="specimen-canvas">
            <template v-if="selectedId === 'buttons'"
              ><UiButton
                :variant="variant"
                :disabled="disabled"
                @click="
                  feedback = 'Action received. This is a component preview.'
                "
                >Save changes<AppIcon name="arrow" /></UiButton
              ><UiButton
                variant="secondary"
                @click="feedback = 'Changes cancelled in this preview.'"
                >Cancel</UiButton
              ></template
            >
            <template v-else-if="selectedId === 'status'"
              ><StatusBadge status="Healthy" /><StatusBadge
                status="Degraded" /><StatusBadge status="Offline"
            /></template>
            <div v-else-if="selectedId === 'inputs'" class="input-specimen">
              <label for="preview-search">Device search</label
              ><label class="search"
                ><AppIcon name="search" /><input
                  id="preview-search"
                  v-model="search"
                  placeholder="Search name or IP address…" /></label
              ><label for="preview-health">Health</label
              ><select id="preview-health" v-model="health">
                <option>All statuses</option>
                <option>Healthy</option>
                <option>Degraded</option>
                <option>Offline</option>
              </select>
              <p aria-live="polite">
                {{ search ? `Searching for “${search}”` : 'All devices' }} ·
                {{ health }}
              </p>
            </div>
            <div
              v-else-if="selectedId === 'metrics'"
              class="metrics specimen-metrics"
            >
              <MetricCard label="Fleet health" :value="98" unit="%" icon="pulse"
                ><b>156 healthy</b> · 3 need attention</MetricCard
              >
            </div>
            <div v-else class="empty">
              <AppIcon name="search" />
              <h3>No devices match this view</h3>
              <p>Try a different name or IP address.</p>
              <UiButton @click="feedback = 'Search cleared in this preview.'"
                >Clear search</UiButton
              >
            </div>
          </div>
          <div v-if="selectedId === 'buttons'" class="specimen-controls">
            <label for="button-variant">Variant</label
            ><select id="button-variant" v-model="variant">
              <option value="primary">Primary</option>
              <option value="secondary">Secondary</option>
              <option value="ghost">Ghost</option></select
            ><label class="check-control"
              ><input v-model="disabled" type="checkbox" /> Disabled</label
            >
          </div>
          <p v-if="feedback" class="preview-feedback" role="status">
            {{ feedback }}
          </p>
        </section>
        <section class="component-guidance">
          <h3>Usage</h3>
          <p>{{ selected.guidance }}</p>
        </section>
        <form class="component-planning" @submit.prevent="saveDraft">
          <div class="planning-heading">
            <div>
              <span class="eyebrow">DEVELOP TOGETHER</span>
              <h3>Decisions & next steps</h3>
            </div>
            <label
              >Stage<select v-model="stage" @change="editDraft">
                <option v-for="item in stages" :key="item">{{ item }}</option>
              </select></label
            >
          </div>
          <p>{{ selected.question }}</p>
          <label :for="`notes-${selectedId}`">Component notes</label>
          <textarea
            :id="`notes-${selectedId}`"
            v-model="notes"
            rows="4"
            placeholder="What should change? Capture behavior, visual decisions, or the next experiment…"
            @input="editDraft"
          ></textarea>
          <div class="planning-footer">
            <span>Personal draft · stored in this browser</span
            ><UiButton type="submit" variant="primary" size="small"
              >Save draft</UiButton
            >
          </div>
          <p v-if="saved" role="status">{{ saved }}</p>
        </form>
      </div>
    </div>

    <div v-else class="foundation-stack">
      <section class="foundation-panel">
        <div class="foundation-heading">
          <div>
            <span class="eyebrow">01 / TYPOGRAPHY</span>
            <h2>Clear at every size.</h2>
            <p>Inter for the interface. Monospace for device addresses.</p>
          </div>
          <label for="specimen-font"
            >Compare typefaces<select id="specimen-font" v-model="font">
              <option value="Inter Variable">Inter · recommended</option>
              <option value="DM Sans Variable">DM Sans · previous</option>
            </select></label
          >
        </div>
        <div
          class="type-specimen"
          :style="{ fontFamily: `'${font}', sans-serif` }"
        >
          <div class="type-row">
            <span>Page / 28 · 600</span
            ><strong class="type-title">Your network, in view.</strong>
          </div>
          <div class="type-row">
            <span>Body / 14 · 400</span
            ><span>Connected spaces. Clear decisions. Berlin → Hamburg.</span>
          </div>
          <div class="type-row">
            <span>Data / tabular figures</span
            ><span class="type-numbers"
              >98.6% &nbsp; 1,024 &nbsp; 00:42:16</span
            >
          </div>
          <div class="type-row">
            <span>Address / mono</span><code>10.22.0.1 &nbsp; fe80::1</code>
          </div>
        </div>
        <p class="font-research">
          m3connect uses
          <a
            href="https://www.grillitype.com/typeface/gt-standard"
            target="_blank"
            rel="noreferrer"
            >GT Standard</a
          >. Inter is our open-source choice for small interface text and
          numeric data. GT Standard remains a brand option with supplied
          licensed files.
          <a href="https://rsms.me/inter/" target="_blank" rel="noreferrer"
            >Explore Inter’s features ↗</a
          >
        </p>
      </section>
      <section class="foundation-panel">
        <div class="foundation-heading">
          <div>
            <span class="eyebrow">02 / COLOR</span>
            <h2>Color has a job.</h2>
            <p>Neutral surfaces, brand accents, and distinct health states.</p>
          </div>
          <a
            class="text-link"
            href="/design/palette.html"
            target="_blank"
            rel="noreferrer"
            >Full palette ↗</a
          >
        </div>
        <div class="swatch-grid">
          <article v-for="swatch in swatches" :key="swatch.token">
            <div
              class="swatch"
              :style="{ background: `var(${swatch.token})` }"
            ></div>
            <strong>{{ swatch.name }}</strong
            ><code>{{ swatch.token }}</code
            ><small>{{ swatch.purpose }}</small>
          </article>
        </div>
      </section>
      <section class="foundation-panel">
        <span class="eyebrow">03 / RHYTHM & INTERACTION</span>
        <h2>Consistent, without feeling rigid.</h2>
        <div class="rules-grid">
          <div>
            <h3>Space in steps of four</h3>
            <p>
              8px within controls, 16px between related elements, 24px inside
              panels, 32px between sections.
            </p>
            <div class="spacing-specimen">
              <span v-for="space in [4, 8, 16, 24, 32]" :key="space"
                ><i :style="{ width: `${space}px` }"></i>{{ space }}</span
              >
            </div>
          </div>
          <div>
            <h3>Shape follows purpose</h3>
            <p>
              8px corners on controls and 12px on content panels. Status labels
              stay compact. Interactive controls have a visible focus ring.
            </p>
          </div>
          <div>
            <h3>Motion gives feedback</h3>
            <p>
              Short transitions acknowledge actions. Live numbers stay still.
              Reduced motion respects the system preference.
            </p>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<style src="./design/workbench.css"></style>
