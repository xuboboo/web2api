import { Web2API } from "@web2api/sdk";

// Self-hosted only: start the local server with `web2api serve`, then run this example.
//   const client = new Web2API({ apiKey: "<YOUR_API_TOKEN>" });
const client = new Web2API({ baseUrl: "http://localhost:7000" });

const query = "privacy focused search engine";

// /mega/search runs one query across several engines and merges the results.
const { results } = await client.megaSearch({
  text: query,
  engines: ["bing", "duckduckgo"],
  region: "US",
  limit: 10,
});

// Group results by domain to see which sites both engines agree on.
const byDomain = new Map();
for (const result of results) {
  const domain = result.domain;
  if (!domain) continue;

  const stats = byDomain.get(domain) ?? { hits: 0, engines: new Set(), bestRank: Infinity };
  stats.hits += 1;
  stats.engines.add(result.engine);
  stats.bestRank = Math.min(stats.bestRank, result.rank ?? Infinity);
  byDomain.set(domain, stats);
}

console.log(`Domains found for "${query}":\n`);
const ranked = [...byDomain].sort((a, b) => a[1].bestRank - b[1].bestRank);
for (const [domain, stats] of ranked) {
  console.log(`${domain}  (rank ${stats.bestRank}, seen in ${[...stats.engines].join(", ")})`);
}
