<script setup lang="ts">
// SortableTableHead — a table header that sorts its column on click, for the
// server-sorted lists driven by useListSort (issue #320).
import { computed } from 'vue'
import { ChevronDown, ChevronUp, ChevronsUpDown } from '@lucide/vue'
import { TableHead } from '@/components/ui/table'
import type { SortOrder } from '@/composables/useListSort'

const props = defineProps<{
  label: string
  active: boolean
  order: SortOrder
}>()

const emit = defineEmits<{ toggle: [] }>()

const ariaSort = computed(() => {
  if (!props.active) return 'none'
  return props.order === 'asc' ? 'ascending' : 'descending'
})
</script>

<template>
  <TableHead :aria-sort="ariaSort">
    <button type="button"
      class="inline-flex items-center gap-1 hover:text-foreground transition-colors"
      @click="emit('toggle')">
      {{ label }}
      <ChevronDown v-if="active && order === 'desc'" class="w-3.5 h-3.5" />
      <ChevronUp v-else-if="active" class="w-3.5 h-3.5" />
      <ChevronsUpDown v-else class="w-3.5 h-3.5 opacity-40" />
    </button>
  </TableHead>
</template>
