import React from 'react';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';

export default function Home() {
  const { siteConfig } = useDocusaurusContext();
  return (
    <Layout title={siteConfig.title} description={siteConfig.tagline}>
      <main style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', minHeight: '60vh', gap: '1.5rem', padding: '4rem 2rem', textAlign: 'center' }}>
        <img src="/muvee/img/mascot.png" alt="muvee mascot" style={{ width: '260px', borderRadius: '16px' }} />
        <h1 style={{ fontSize: '3rem', fontWeight: 800, margin: 0 }}>muvee</h1>
        <p style={{ fontSize: '1.1rem', opacity: 0.5, margin: '-0.5rem 0 0', letterSpacing: '0.05em' }}>
          Microservices Unified Virtualized Execution Engine
        </p>
        <p style={{ fontSize: '1.25rem', maxWidth: '540px', opacity: 0.75, margin: 0 }}>
          {siteConfig.tagline}
        </p>
        <div style={{ display: 'flex', gap: '1rem', flexWrap: 'wrap', justifyContent: 'center' }}>
          <Link className="button button--primary button--lg" to="/docs/getting-started">
            Get Started →
          </Link>
          <Link className="button button--secondary button--lg" href="https://github.com/hoveychen/muvee">
            GitHub
          </Link>
        </div>
      </main>
    </Layout>
  );
}
