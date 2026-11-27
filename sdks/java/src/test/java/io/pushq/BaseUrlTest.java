package io.pushq;

import static org.junit.jupiter.api.Assertions.assertEquals;

import org.junit.jupiter.api.Test;

class BaseUrlTest {

    @Test
    void defaultsToHostedApi() {
        assertEquals("https://api.pushqueue.dev", Pushq.DEFAULT_BASE_URL);
        assertEquals(Pushq.DEFAULT_BASE_URL, Pushq.builder().apiKey("k").build().getBaseUrl());
        assertEquals(Pushq.DEFAULT_BASE_URL, Pushq.builder().baseUrl(" ").build().getBaseUrl());
    }

    @Test
    void usesExplicitBaseUrl() {
        assertEquals("http://localhost:8080",
                Pushq.builder().baseUrl("http://localhost:8080/").build().getBaseUrl());
    }
}
