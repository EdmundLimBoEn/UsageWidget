export default {
  name: "usagewidget-install",
  compatibilityDate: "2026-07-19",
  entrypoint: "./worker/install.js",
  workersDev: true,
  triggers: [
    {
      type: "fetch",
      pattern: "usagewidget.edmundlim.systems/install.sh",
      zone: "edmundlim.systems"
    },
    {
      type: "fetch",
      pattern: "usagewidget.edmundlim.systems/",
      zone: "edmundlim.systems"
    }
  ]
};
