from web2api import Web2API

with Web2API(base_url="https://radar.ydsdoa.cn") as client:
    response = client.search(
        engine="google",
        text="open source search api",
        region="US",
        lang="EN",
        limit=10,
    )

    for item in response.results:
        print(f"{item.rank}. {item.title}")
        print(f"   {item.url}")

    if client.last_response:
        print("Request ID:", client.last_response.request_id)
