<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import darkMark from '../assets/meridian-deck-icon-dark.png'
import lightMark from '../assets/meridian-deck-icon.png'

const props = defineProps<{ variant: 'echo' | 'orbit'; dark: boolean }>()
const motion = matchMedia('(prefers-reduced-motion: reduce)')
const reduced = ref(motion.matches)
const vector = typeof SVGGeometryElement !== 'undefined' && typeof Element.prototype.getAnimations === 'function' &&
  typeof CSS !== 'undefined' && CSS.supports('transform-box', 'fill-box')
const gifs = import.meta.glob<string>('../assets/startup-*.gif', { eager: true, query: '?url', import: 'default' })
const gif = computed(() => gifs[`../assets/startup-${props.variant}-${props.dark ? 'dark' : 'light'}.gif`])
const failedGif = ref('')
function motionChanged(event: MediaQueryListEvent) { reduced.value = event.matches }
onMounted(() => motion.addEventListener('change', motionChanged))
onBeforeUnmount(() => motion.removeEventListener('change', motionChanged))
</script>

<template>
  <div class="startup-art" :class="variant" :data-theme="dark ? 'dark' : 'light'" role="img" aria-label="子午线，一线通山海">
    <svg v-if="vector && !reduced" viewBox="0 0 560 360" aria-hidden="true">
      <g transform="translate(216 58)">
        <circle v-if="variant === 'orbit'" class="ticks" cx="64" cy="64" r="64" stroke-dasharray="1.4 5.3" />
        <g v-if="variant === 'echo'" transform="translate(90 64)">
          <circle class="wave first" r="32"/><circle class="wave second" r="32"/>
        </g>
        <g class="meridian" fill="none" stroke-width="10">
          <path pathLength="1" d="M111 45 A50 50 0 1 0 108 90" />
          <path pathLength="1" d="M63 14 C33 40 35 86 61 114" />
        </g>
        <g class="prompt" fill="var(--ink-blue)">
          <path d="M62 40 L95 64 L62 88 V75 L78 64 L62 53 Z"/>
          <path class="cursor" d="M89 75 H119 V85 H89 Z"/>
        </g>
      </g>
      <g class="wordmark" text-anchor="middle" font-family="'Segoe UI','PingFang SC','Microsoft YaHei',sans-serif">
        <text class="name" x="280" y="239" font-size="34" font-weight="650">子午线</text>
        <text class="tagline" x="280" y="276" font-size="17" letter-spacing="2">一线通山海</text>
        <svg v-if="variant === 'orbit'" x="218" y="201" width="124" height="84" overflow="hidden">
          <defs><clipPath id="startup-lettering"><text x="62" y="38" font-size="34" font-weight="650">子午线</text><text x="62" y="75" font-size="17" letter-spacing="2">一线通山海</text></clipPath></defs>
          <g clip-path="url(#startup-lettering)"><path class="glint" d="M-54 0 H-37 L0 84 H-17 Z"/></g>
        </svg>
      </g>
    </svg>
    <img v-else-if="!reduced && gif && failedGif !== gif" class="gif" :src="gif" alt="" @error="failedGif = gif">
    <div v-else class="poster" aria-hidden="true">
      <img :src="dark ? darkMark : lightMark" alt=""><strong>子午线</strong><span>一线通山海</span>
    </div>
  </div>
</template>

<style scoped>
.startup-art {
  --paper: #fff; --ink-mark: #1f324d; --ink-blue: #2855d6; --ink-text: #111827; --ink-muted: #5b677b; --ink-glint: #8495af;
  width: min(560px, 100%); aspect-ratio: 14 / 9; overflow: hidden; background: var(--paper); color: var(--ink-text);
}
.startup-art[data-theme="dark"] {
  --paper: #0a0c10; --ink-mark: #eaf1fc; --ink-blue: #6ea8fe; --ink-text: #edf2fb; --ink-muted: #8a97ae; --ink-glint: #fff;
}
.startup-art > svg, .gif { display: block; width: 100%; height: 100%; }
.meridian { stroke: var(--ink-mark); }
.name { fill: var(--ink-text); }
.tagline { fill: var(--ink-muted); }
.poster { height: 100%; display: flex; align-items: center; justify-content: center; flex-direction: column; gap: 8px; font-family: 'Segoe UI','Microsoft YaHei',sans-serif; }
.poster img { width: 23%; margin-bottom: 10px; }
.poster strong { font-size: 1.9em; }
.poster span { color: var(--ink-muted); }
.echo .meridian path { stroke-dasharray: 1; animation: draw .55s cubic-bezier(.22,1,.36,1) both; }
.echo .prompt { animation: prompt-in .35s .45s cubic-bezier(.16,1,.3,1) both; }
.echo .cursor { animation: blink .4s .85s linear both; }
.echo .wordmark { animation: rise .4s .85s ease-out both; }
.wave, .ticks { fill: none; stroke: var(--ink-blue); }
.wave { stroke-width: 1.4; transform-box: fill-box; transform-origin: center; }
.wave.first { animation: wave .59s .72s ease-out both; }
.wave.second { animation: wave .59s .86s ease-out both; }
.ticks { transform-box: fill-box; transform-origin: center; animation: align .70s ease-out both; }
.orbit .meridian { animation: appear .7s ease-out both; }
.orbit .prompt { animation: prompt-in .45s .55s cubic-bezier(.16,1,.3,1) both; }
.orbit .wordmark { animation: appear .45s .55s ease-out both; }
.glint { fill: var(--ink-glint); animation: sweep .55s 1s ease-in-out both; }
@keyframes draw { from { stroke-dashoffset: 1; } to { stroke-dashoffset: 0; } }
@keyframes appear { from { opacity: 0; } to { opacity: 1; } }
@keyframes prompt-in { from { opacity: 0; transform: translateX(-9px); } to { opacity: 1; transform: translateX(0); } }
@keyframes rise { from { opacity: 0; transform: translateY(7px); } to { opacity: 1; transform: translateY(0); } }
@keyframes blink { 0%, 48%, 80% { opacity: .12; } 22%, 63%, 100% { opacity: 1; } }
@keyframes wave { 0% { transform: scale(.55); opacity: 0; } 15% { opacity: .65; } 100% { transform: scale(2.1); opacity: 0; } }
@keyframes align { from { transform: rotate(-42deg); opacity: .65; } to { transform: rotate(0); opacity: 0; } }
@keyframes sweep { from { transform: translateX(0); } to { transform: translateX(180px); } }
</style>
