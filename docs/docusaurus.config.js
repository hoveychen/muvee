// @ts-check
/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'muvee',
  tagline: 'Lightweight self-hosted PaaS with smart data warehouse integration',
  favicon: 'img/favicon.ico',

  url: 'https://hoveychen.github.io',
  baseUrl: '/muvee/',

  organizationName: 'hoveychen',
  projectName: 'muvee',

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',

  // Force dark mode — matches the frontend's pure-dark aesthetic
  headTags: [
    {
      tagName: 'link',
      attributes: {
        rel: 'preconnect',
        href: 'https://fonts.googleapis.com',
      },
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'preconnect',
        href: 'https://fonts.gstatic.com',
        crossorigin: 'anonymous',
      },
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'stylesheet',
        href: 'https://fonts.googleapis.com/css2?family=DM+Mono:ital,wght@0,300;0,400;0,500;1,300;1,400&family=Lora:ital,wght@0,400;0,600;0,700;1,400;1,600&display=swap',
      },
    },
  ],

  i18n: {
    defaultLocale: 'en',
    locales: ['en', 'zh'],
    localeConfigs: {
      en: { label: 'English' },
      zh: { label: '中文' },
    },
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          sidebarPath: './sidebars.js',
          editUrl: 'https://github.com/hoveychen/muvee/tree/main/docs/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      }),
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      colorMode: {
        defaultMode: 'dark',
        disableSwitch: true,
        respectPrefersColorScheme: false,
      },
      navbar: {
        title: 'muvee',
        logo: {
          alt: 'muvee Logo',
          src: 'img/logo.svg',
        },
        style: 'dark',
        items: [
          {
            type: 'docSidebar',
            sidebarId: 'tutorialSidebar',
            position: 'left',
            label: 'Docs',
          },
          {
            type: 'localeDropdown',
            position: 'right',
          },
          {
            href: 'https://github.com/hoveychen/muvee',
            label: 'GitHub',
            position: 'right',
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Docs',
            items: [
              { label: 'Getting Started', to: '/docs/getting-started' },
              { label: 'Architecture', to: '/docs/architecture' },
            ],
          },
          {
            title: 'Community',
            items: [
              { label: 'GitHub Issues', href: 'https://github.com/hoveychen/muvee/issues' },
              { label: 'Discussions', href: 'https://github.com/hoveychen/muvee/discussions' },
            ],
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} The muvee Authors. Licensed under Apache 2.0.`,
      },
      prism: {
        theme: { plain: { color: '#e8e4dc', backgroundColor: '#161616' }, styles: [] },
        darkTheme: { plain: { color: '#e8e4dc', backgroundColor: '#161616' }, styles: [] },
        additionalLanguages: ['bash', 'yaml', 'docker', 'sql'],
      },
    }),
};

module.exports = config;
