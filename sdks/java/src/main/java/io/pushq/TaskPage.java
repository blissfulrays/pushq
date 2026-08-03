package io.pushq;

import com.fasterxml.jackson.annotation.JsonProperty;
import java.util.Collections;
import java.util.List;

/** One page of {@link Pushq#listTasks} results (newest first). */
public final class TaskPage {

    @JsonProperty("tasks")
    private List<Task> tasks;

    @JsonProperty("next_page_token")
    private String nextPageToken;

    public TaskPage() {
    }

    /** Tasks on this page; never {@code null}. */
    public List<Task> getTasks() {
        return tasks == null ? Collections.emptyList() : tasks;
    }

    /** Token for the next page, or {@code null}/empty when this is the last page. */
    public String getNextPageToken() {
        return nextPageToken;
    }
}
