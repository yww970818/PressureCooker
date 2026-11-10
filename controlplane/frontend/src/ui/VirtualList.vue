<script setup>
import {computed,onMounted,onBeforeUnmount,ref,watch} from 'vue';
const props=defineProps({items:{type:Array,default:()=>[]},rowHeight:{type:Number,default:40},itemKey:{type:Function,default:(_item,index)=>index},height:{type:String,default:'100%'}});
const host=ref(null),top=ref(0),size=ref(420);let observer;
const start=computed(()=>Math.max(0,Math.floor(top.value/props.rowHeight)-6));
const end=computed(()=>Math.min(props.items.length,start.value+Math.ceil(size.value/props.rowHeight)+12));
const visible=computed(()=>props.items.slice(start.value,end.value));
watch(()=>props.items,()=>{if(host.value && top.value>props.items.length*props.rowHeight){host.value.scrollTop=0;top.value=0;}});
onMounted(()=>{const update=()=>{size.value=host.value?.clientHeight||420;};update();if(globalThis.ResizeObserver){observer=new ResizeObserver(update);observer.observe(host.value);}});
onBeforeUnmount(()=>observer?.disconnect());
</script>
<template><div ref="host" class="virtual-list" :style="{height}" @scroll.passive="top=$event.target.scrollTop"><div :style="{paddingTop:`${start*rowHeight}px`,paddingBottom:`${Math.max(0,items.length-end)*rowHeight}px`}"><div v-for="(item,index) in visible" :key="itemKey(item,start+index)" :style="{height:`${rowHeight}px`}" class="virtual-list-row"><slot :item="item" :index="start+index"/></div></div></div></template>
<style scoped>.virtual-list{min-height:0;overflow:auto;overflow-anchor:none;contain:strict;flex:1}.virtual-list-row{box-sizing:border-box;min-width:0}.virtual-list-row>:deep(*){max-width:100%;box-sizing:border-box}</style>
