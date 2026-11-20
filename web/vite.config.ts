import {defineConfig} from 'vite';
import vue from '@vitejs/plugin-vue';
// Development only: AEL_DEV_SERVER points the Vite dev server at a disposable
// local AEL Server. Requests keep the Server's canonical Origin; production
// builds and the embedded dashboard are unaffected.
const target=process.env.AEL_DEV_SERVER;
export default defineConfig({plugins:[vue()],build:{outDir:'../internal/server/ui',emptyOutDir:true},
  server:target?{proxy:{'/api':{target,changeOrigin:true,configure:(proxy)=>{proxy.on('proxyReq',(req)=>{if(req.getHeader('origin'))req.setHeader('origin',target);});}}}}:undefined});
