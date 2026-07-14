const { chromium } = require('C:/Users/31031/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/.pnpm/playwright@1.61.1/node_modules/playwright');

(async () => {
  const browser = await chromium.launch({
    executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe',
    headless: true,
  });
  const viewports = [
    { name: 'desktop', width: 1280, height: 900 },
    { name: 'mobile', width: 390, height: 844 },
  ];
  for (const viewport of viewports) {
    const page = await browser.newPage({ viewport });
    await page.goto('http://127.0.0.1:18081/', { waitUntil: 'networkidle' });
    await page.screenshot({
      path: `D:/codes/TvLink/.superpowers/monitor-preview/${viewport.name}.png`,
      fullPage: true,
    });
    const metrics = await page.evaluate(() => ({
      viewport: { width: innerWidth, height: innerHeight },
      document: {
        scrollWidth: document.documentElement.scrollWidth,
        scrollHeight: document.documentElement.scrollHeight,
      },
      progress: [...document.querySelectorAll('.usage-progress')].map((element) => ({
        rail: element.getBoundingClientRect().toJSON(),
        actual: element.querySelector('.progress-actual').getBoundingClientRect().toJSON(),
        projected: element.querySelector('.progress-projected').getBoundingClientRect().toJSON(),
      })),
    }));
    console.log(JSON.stringify({ name: viewport.name, ...metrics }));
    await page.close();
  }
  await browser.close();
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
