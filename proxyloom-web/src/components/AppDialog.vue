<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
defineProps<{ title: string }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
let previousFocus: HTMLElement | null = null
onMounted(() => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  dialog.value?.showModal()
})
onBeforeUnmount(() => { dialog.value?.close(); previousFocus?.focus() })
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="dialog-title" @cancel.prevent="emit('close')">
      <div class="dialog-heading"><h2 id="dialog-title">{{ title }}</h2><button class="icon-button" type="button" aria-label="关闭对话框" @click="emit('close')">×</button></div>
      <slot />
    </dialog>
  </Teleport>
</template>
