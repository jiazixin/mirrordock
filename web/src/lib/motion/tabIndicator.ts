import type { Directive } from 'vue'

// Replaces each tab's own underline with one bar on the tab list that slides to the active tab.
// The active tab is the router's exact-active link or a child carrying the `active` class.
const activeTab = ':scope > .router-link-exact-active, :scope > .active'

interface Tracker {
  mutations: MutationObserver
  resizes: ResizeObserver
  frame: number
}

const trackers = new WeakMap<HTMLElement, Tracker>()

function place(list: HTMLElement) {
  const tab = list.querySelector<HTMLElement>(activeTab)
  list.style.setProperty('--tab-x', `${tab?.offsetLeft ?? 0}px`)
  list.style.setProperty('--tab-w', `${tab?.offsetWidth ?? 0}px`)
}

function observeTabs(list: HTMLElement, resizes: ResizeObserver) {
  resizes.disconnect()
  resizes.observe(list)
  for (const tab of list.children) resizes.observe(tab)
}

export const vTabIndicator: Directive<HTMLElement> = {
  mounted(list) {
    const resizes = new ResizeObserver(() => place(list))
    const mutations = new MutationObserver((records) => {
      if (records.some((record) => record.type === 'childList')) observeTabs(list, resizes)
      place(list)
    })
    mutations.observe(list, { childList: true, subtree: true, attributeFilter: ['class'] })
    observeTabs(list, resizes)
    list.classList.add('tab-indicator')
    place(list)
    // Enable the slide only after the first position is painted, so the bar never sweeps in from 0.
    const frame = requestAnimationFrame(() => list.classList.add('tab-indicator-ready'))
    trackers.set(list, { mutations, resizes, frame })
  },
  unmounted(list) {
    const tracker = trackers.get(list)
    if (!tracker) return
    cancelAnimationFrame(tracker.frame)
    tracker.mutations.disconnect()
    tracker.resizes.disconnect()
    trackers.delete(list)
  },
}
