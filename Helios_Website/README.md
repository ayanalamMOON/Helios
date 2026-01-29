# HELIOS Documentation Website

A modern, minimalist documentation website for the HELIOS framework, built with [Astro](https://astro.build) and [Bun](https://bun.sh).

## Quick Start

### Prerequisites

- [Bun](https://bun.sh) v1.0 or later

### Installation

```bash
# Navigate to the website directory
cd Helios_Website

# Install dependencies
bun install
```

### Development

```bash
# Start the development server
bun run dev
```

The site will be available at `http://localhost:4321`

### Build

```bash
# Build for production
bun run build

# Preview production build
bun run preview
```

## Project Structure

```
Helios_Website/
├── public/              # Static assets
│   └── favicon.svg
├── src/
│   ├── components/      # Reusable Astro components
│   │   └── CodeBlock.astro
│   ├── layouts/         # Page layouts
│   │   └── Layout.astro
│   ├── pages/           # File-based routing
│   │   ├── index.astro
│   │   └── docs/
│   │       ├── quickstart.astro
│   │       ├── architecture.astro
│   │       └── api.astro
│   └── styles/          # Global styles
│       └── global.css
├── astro.config.mjs     # Astro configuration
├── package.json
└── tsconfig.json
```

## Design System

The site uses a minimalist dark theme with:

- **Font**: Inter (sans-serif), JetBrains Mono (monospace)
- **Colors**: Dark background with blue/purple/orange accents
- **Layout**: Responsive with mobile-first approach
- **Components**: Modular and reusable

## Pages

- **Home** (`/`) - Landing page with features and quick example
- **Quick Start** (`/docs/quickstart`) - Getting started guide
- **Architecture** (`/docs/architecture`) - System architecture documentation
- **API Reference** (`/docs/api`) - Complete API documentation

## Built With

- **[Astro](https://astro.build)** - Static site generator
- **[Bun](https://bun.sh)** - JavaScript runtime and package manager
- **[@astrojs/mdx](https://docs.astro.build/en/guides/integrations-guide/mdx/)** - MDX support
- **[@astrojs/sitemap](https://docs.astro.build/en/guides/integrations-guide/sitemap/)** - Sitemap generation

## Features

- Lightning-fast static site generation
- Minimalist, modern design
- Fully responsive
- Dark theme optimized
- Syntax highlighting for code blocks
- SEO optimized
- Accessible

## Adding New Documentation

1. Create a new `.astro` file in `src/pages/docs/`
2. Use the `Layout` component for consistent styling
3. Add navigation links in the respective pages

Example:

```astro
---
import Layout from '../../layouts/Layout.astro';
---

<Layout title="New Page" description="Page description">
  <div class="container">
    <div class="docs-content">
      <h1>New Documentation Page</h1>
      <p>Content goes here...</p>
    </div>
  </div>
</Layout>
```

## Deployment

The site can be deployed to any static hosting service:

- **Vercel**: Connect your GitHub repo
- **Netlify**: Drag and drop the `dist/` folder
- **GitHub Pages**: Use GitHub Actions workflow
- **Cloudflare Pages**: Connect and deploy

Build command: `bun run build`
Output directory: `dist/`

## License

Same license as the HELIOS framework.
