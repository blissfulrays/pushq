package io.pushq;

import com.fasterxml.jackson.annotation.JsonProperty;
import java.util.Collections;
import java.util.List;

/** One page of {@link Pushq#listQueues} results. */
public final class QueuePage {

    @JsonProperty("queues")
    private List<Queue> queues;

    @JsonProperty("next_page_token")
    private String nextPageToken;

    public QueuePage() {
    }

    /** Queues on this page; never {@code null}. */
    public List<Queue> getQueues() {
        return queues == null ? Collections.emptyList() : queues;
    }

    /** Token for the next page, or {@code null}/empty when this is the last page. */
    public String getNextPageToken() {
        return nextPageToken;
    }
}
