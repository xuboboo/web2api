from web2api import Web2API

url = "https://go.dev/doc/"
output_file = "extracted.md"

# Self-hosted only: start the local server with `web2api serve`, then run this example.
#   with Web2API(api_key="<YOUR_API_TOKEN>", timeout=60.0) as client:
with Web2API(base_url="http://localhost:7000", timeout=60.0) as client:
    # /extract turns a single page into clean Markdown (or plain text).
    result = client.extract(url=url, mode="auto", clean=True)

content = result.markdown or result.text or ""
with open(output_file, "w", encoding="utf-8") as handle:
    handle.write(content)

print(f"Extracted {len(content)} characters from {url} into {output_file}")
