import { Web2API } from "@web2api/sdk";

// Self-hosted only: start the local server with `web2api serve`, then run this example.
//   const client = new Web2API({ apiKey: "<YOUR_API_TOKEN>" });
const client = new Web2API({ baseUrl: "http://localhost:7000" });

// Domains that rank for several of your keywords are your real SERP competitors.
const keywords = ["serp api", "google search api", "scrape search results"];

const byDomain = new Map();
for (const keyword of keywords) {
  const { results } = await client.search({
    engine: "google",
    text: keyword,
    region: "US",
    limit: 10,
  });

  for (const result of results) {
    if (!result.domain) continue;
    const stats = byDomain.get(result.domain) ?? { keywords: new Set(), bestRank: Infinity };
    stats.keywords.add(keyword);
    stats.bestRank = Math.min(stats.bestRank, result.rank ?? Infinity);
    byDomain.set(result.domain, stats);
  }
}

console.log(`Competitor overlap across ${keywords.length} keywords:\n`);
const ranked = [...byDomain].sort((a, b) => b[1].keywords.size - a[1].keywords.size);
for (const [domain, stats] of ranked) {
  console.log(`${domain}  —  ${stats.keywords.size}/${keywords.length} keywords, best rank ${stats.bestRank}`);
}
