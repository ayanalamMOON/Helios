import mdx from '@astrojs/mdx';
import sitemap from '@astrojs/sitemap';
import { defineConfig } from 'astro/config';

export default defineConfig({
    site: 'https://helios.dev',
    integrations: [mdx(), sitemap()],
    markdown: {
        shikiConfig: {
            theme: 'github-dark-dimmed',
            wrap: false
        }
    }
});
