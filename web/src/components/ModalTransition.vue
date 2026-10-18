<!-- Teleports a dialog to body and animates it in and out. Wrap the v-if'd dialog component
     here: a Transition placed inside the dialog itself never sees it unmount. -->
<template>
  <Teleport to="body">
    <Transition name="dialog"><slot /></Transition>
  </Teleport>
</template>
