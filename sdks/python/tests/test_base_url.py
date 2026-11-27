import asyncio

from pushq import DEFAULT_BASE_URL, AsyncPushq, Pushq


def test_default_base_url():
    assert DEFAULT_BASE_URL == "https://api.pushqueue.dev"
    with Pushq(api_key="k") as pq:
        assert str(pq._http.base_url).rstrip("/") == DEFAULT_BASE_URL
    with Pushq("", api_key="k") as pq:
        assert str(pq._http.base_url).rstrip("/") == DEFAULT_BASE_URL


def test_explicit_base_url():
    with Pushq("http://localhost:8080/", api_key="k") as pq:
        assert str(pq._http.base_url).rstrip("/") == "http://localhost:8080"


def test_async_default_base_url():
    async def run():
        pq = AsyncPushq(api_key="k")
        try:
            assert str(pq._http.base_url).rstrip("/") == DEFAULT_BASE_URL
        finally:
            await pq.aclose()

    asyncio.run(run())
