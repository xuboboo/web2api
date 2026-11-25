import { Web2API } from "@web2api/sdk";

const client = new Web2API({ baseUrl: "https://radar.ydsdoa.cn" });

const { results } = await client.search({
  engine: "google",
  text: "open source search api",
  region: "US",
  lang: "EN",
  limit: 10,
});

for (const item of results) {
  console.log(`${item.rank}. ${item.title}`);
  console.log(`   ${item.url}`);
}

console.log("Request ID:", client.lastResponse?.requestId);
