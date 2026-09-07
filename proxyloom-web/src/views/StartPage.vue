<script setup lang="ts">
import { useStartStore } from '../stores/start'

const start = useStartStore()
</script>

<template>
  <div class="page">
    <header class="site-header">
      <a class="brand" href="/" aria-label="ProxyLoom 首页">
        <span class="brand-mark" aria-hidden="true">P</span>
        ProxyLoom
      </a>
      <span class="header-note">工程启动页</span>
    </header>

    <main>
      <section class="intro" aria-labelledby="page-title">
        <p class="eyebrow">自托管 · 订阅管理与测试平台</p>
        <h1 id="page-title">从清晰的边界开始。</h1>
        <p class="lead">面向 Xray、sing-box 与 Mihomo，逐步构建可追溯的配置管理、发布与测试流程。</p>
        <div class="notice">
          <span class="notice-dot" aria-hidden="true"></span>
          <div>
            <strong>尚未接入业务功能</strong>
            <p>当前仅展示工程结构，服务运行状态尚未检测。</p>
          </div>
        </div>
      </section>

      <section aria-labelledby="structure-title">
        <div class="section-heading">
          <h2 id="structure-title">三个单元，职责分明</h2>
          <span>工程结构</span>
        </div>
        <div class="services">
          <article class="service">
            <span class="service-number">01</span>
            <h3>管理界面</h3>
            <p>承载浏览器交互，当前为中文工程启动页。</p>
            <span class="technology">Vue 3 · TypeScript</span>
          </article>
          <article class="service">
            <span class="service-number">02</span>
            <h3>API 服务</h3>
            <p>作为独立服务，逐步接入资源管理与发布流程。</p>
            <span class="technology">Go · PostgreSQL</span>
          </article>
          <article class="service">
            <span class="service-number">03</span>
            <h3>独立执行器</h3>
            <p>独立部署，后续承载受控的内核校验与网络测试。</p>
            <span class="technology">Go · Runner</span>
          </article>
        </div>
        <div class="boundaries">
          <button
            type="button"
            :aria-expanded="start.showBoundaries"
            aria-controls="boundary-details"
            @click="start.toggleBoundaries"
          >
            {{ start.showBoundaries ? '收起职责边界' : '查看职责边界' }}
            <span aria-hidden="true">{{ start.showBoundaries ? '−' : '+' }}</span>
          </button>
          <div v-if="start.showBoundaries" id="boundary-details" class="boundary-details">
            API 不直接运行内核。Runner 不访问数据库，不持有全局主密钥。
            此页面不执行订阅发布或网络测试，也不代表这些功能已经实现。
          </div>
        </div>
      </section>
    </main>

    <footer>
      <span>ProxyLoom</span>
      <span>基础工程阶段 · 单工作空间</span>
    </footer>
  </div>
</template>
