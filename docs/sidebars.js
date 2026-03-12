/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  tutorialSidebar: [
    {
      type: 'category',
      label: 'Getting Started',
      items: [
        'getting-started',
        'installation',
        'configuration',
      ],
    },
    {
      type: 'category',
      label: 'Architecture',
      items: [
        'architecture',
        'scheduler',
        'dataset-monitor',
        'forward-auth',
      ],
    },
    {
      type: 'category',
      label: 'Operations',
      items: [
        'agents',
        'traefik',
        'upgrade',
      ],
    },
  ],
};

module.exports = sidebars;
