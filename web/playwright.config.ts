import { defineConfig } from '@playwright/test'
export default defineConfig({ testDir:'./e2e', timeout:60000, workers:1, fullyParallel:false, use:{viewport:{width:1440,height:1000},trace:'retain-on-failure'}, reporter:[['list'],['html',{open:'never'}]], outputDir:'test-results' })
