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
      label: 'Authentication',
      items: [
        'auth/auth-google',
        'auth/auth-feishu',
        'auth/auth-wecom',
        'auth/auth-dingtalk',
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
        'service-auth-integration',
      ],
    },
    {
      type: 'category',
      label: 'Operations',
      items: [
        'agents',
        'traefik',
        'secrets',
        'upgrade',
        'muveectl',
        'ai-agent',
      ],
    },
  ],
};

module.exports = sidebars;
