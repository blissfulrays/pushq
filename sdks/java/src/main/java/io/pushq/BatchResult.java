package io.pushq;

import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * Per-item result of a {@link Pushq#createTasks} batch, in request order.
 * Exactly one of {@link #getTask()} / {@link #getError()} is set.
 */
public final class BatchResult {

    @JsonProperty("index")
    private int index;

    @JsonProperty("task")
    private Task task;

    @JsonProperty("error")
    private Error error;

    public BatchResult() {
    }

    /** Position of this item in the submitted batch. */
    public int getIndex() {
        return index;
    }

    /** The created task, or {@code null} if this item failed. */
    public Task getTask() {
        return task;
    }

    /** The per-item error, or {@code null} if this item succeeded. */
    public Error getError() {
        return error;
    }

    public boolean isSuccess() {
        return error == null;
    }

    /** Per-item batch error. */
    public static final class Error {

        @JsonProperty("code")
        private String code;

        @JsonProperty("detail")
        private String detail;

        public Error() {
        }

        /** Stable machine code, e.g. {@code already_exists}, {@code invalid_argument}. */
        public String getCode() {
            return code;
        }

        public String getDetail() {
            return detail;
        }
    }
}
