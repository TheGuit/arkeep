// useListSort.ts — Sort state of a server-sorted, paginated list (issue #320).
//
// The sort column and direction live in the URL query (?sort=&order=), so they
// survive a reload and travel with a shared link. Without ?sort= the server
// applies the list's default order; pass it as defaults so the matching header
// shows as active.

import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

export type SortOrder = 'asc' | 'desc'

export function useListSort(defaults: { key?: string; order?: SortOrder } = {}) {
  const route = useRoute()
  const router = useRouter()

  const querySort = computed(() => (typeof route.query.sort === 'string' ? route.query.sort : ''))

  const sortKey = computed(() => querySort.value || defaults.key || '')
  const sortOrder = computed<SortOrder>(() => {
    if (!querySort.value) return defaults.order ?? 'asc'
    return route.query.order === 'desc' ? 'desc' : 'asc'
  })

  // Appended to the list request; empty while the server default applies.
  const queryParams = computed(() =>
    querySort.value ? `&sort=${encodeURIComponent(querySort.value)}&order=${sortOrder.value}` : '',
  )

  // A column not sorted yet sorts in firstOrder, a second click reverses it, a
  // third goes back to the list's default order.
  function toggle(key: string, firstOrder: SortOrder = 'asc') {
    const rest = { ...route.query }
    delete rest.sort
    delete rest.order
    let next = {}
    if (sortKey.value !== key) {
      next = { sort: key, order: firstOrder }
    } else if (sortOrder.value === firstOrder) {
      next = { sort: key, order: firstOrder === 'asc' ? 'desc' : 'asc' }
    }
    router.replace({ query: { ...rest, ...next } })
  }

  return { sortKey, sortOrder, queryParams, toggle }
}
