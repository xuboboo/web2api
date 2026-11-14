import { Web2API } from "@web2api/sdk";

// Hosted API instead? Get a key at https://web2api.org/dashboard/keys:
//   const client = new Web2API({ apiKey: "<YOUR_API_TOKEN>" });
const client = new Web2API({ baseUrl: "http://localhost:7000" });

const question = "What is retrieval augmented generation?";

const { results } = await client.search({
  engine: "google",
  text: question,
  limit: 10,
});

// Format the top results as numbered, citable context to drop into an LLM prompt.
const context = results
  .map((r, i) => `[${i + 1}] ${r.title}\n${r.url}\n${r.snippet ?? ""}`)
  .join("\n\n");

const prompt = `Answer the question using only the search results below. Cite sources as [n].

Question: ${question}

Search results:
${context}`;

console.log(prompt);
