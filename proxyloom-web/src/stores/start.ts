import { ref } from 'vue'
import { defineStore } from 'pinia'

export const useStartStore = defineStore('start', () => {
  const showBoundaries = ref(false)

  function toggleBoundaries() {
    showBoundaries.value = !showBoundaries.value
  }

  return { showBoundaries, toggleBoundaries }
})
