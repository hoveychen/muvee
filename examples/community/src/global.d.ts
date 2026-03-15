declare global {
  interface Window {
    /** Base URL of the Muvee control panel, e.g. "https://example.com" */
    MUVEE_API_BASE?: string
    /** Full URL to the control-panel dashboard, e.g. "https://example.com/projects" */
    MUVEE_DASHBOARD_URL?: string
  }
}

export {}
