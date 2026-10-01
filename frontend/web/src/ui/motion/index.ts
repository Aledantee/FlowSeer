import { Motion, MotionConfig } from 'motion-v'
import type { Component } from 'vue'

// motion-v augments Vue HTML attributes with a dragControls type that is
// incompatible with Vue Flow's node attributes.
declare module 'vue' {
  interface HTMLAttributes {
    dragControls?: never
  }
}

export const UiMotion: Component = Motion
export const UiMotionConfig = MotionConfig
export { useMotionFeedback } from './useMotionFeedback'
