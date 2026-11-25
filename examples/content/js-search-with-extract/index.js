import { Web2API } from "@web2api/sdk";

// Self-hosted only: start the local server with `web2api serve`, then run this example.
//   const client = new Web2API({ apiKey: "<YOUR_API_TOKEN>", timeoutMs: 60_000 });
const client = new Web2API({ baseUrl: "https://radar.ydsdoa.cn", timeoutMs: 60_000 });

// `extract: N` fetches up to N organic pages (max 5) and returns their cleaned
// content alongside each result, so you get the page text in a single request.
// `extract: true` is shorthand for the top organic result.
const { results } = await client.search({
  engine: "ecosia",
  text: "what is a serp api",
  extract: 2,
  extractMode: "auto",
});

for (const item of results) {
  console.log(`${item.rank}. ${item.title}`);
  console.log(`   ${item.url}`);

  // When extracted, `item.extracted.content` holds the page body. Trim it to
  // a short preview here.
  const content = item.extracted?.content;
  if (content) {
    console.log(`   ${content.slice(0, 300).trim()}…`);
  }
  console.log();
}
