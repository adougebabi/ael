import {defineConfig} from '@playwright/test';
export default defineConfig({testDir:'./tests',workers:1,use:{baseURL:process.env.AEL_TEST_URL||'http://127.0.0.1:7331',viewport:{width:1680,height:1040}},outputDir:'/tmp/ael-browser-results',reporter:[['list'],['json',{outputFile:'/tmp/ael-browser-results/report.json'}]]});
