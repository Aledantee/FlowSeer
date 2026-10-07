// The login page animates its content in when a visit starts on it. Reached
// from the console it is morphed into instead (`morph.ts`), and an entrance
// on top of that would hide the content the morph is moving. True only for
// the first call.
let first = true

export function takeEntrance(): boolean {
  const entering = first
  first = false
  return entering
}
