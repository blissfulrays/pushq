package io.pushq;

/** Optional filters and pagination for {@link Pushq#listQueues}. */
public final class ListQueuesParams {

    private final String namePrefix;
    private final Integer pageSize;
    private final String pageToken;

    private ListQueuesParams(Builder b) {
        this.namePrefix = b.namePrefix;
        this.pageSize = b.pageSize;
        this.pageToken = b.pageToken;
    }

    public static Builder builder() {
        return new Builder();
    }

    public String getNamePrefix() {
        return namePrefix;
    }

    public Integer getPageSize() {
        return pageSize;
    }

    public String getPageToken() {
        return pageToken;
    }

    public static final class Builder {
        private String namePrefix;
        private Integer pageSize;
        private String pageToken;

        private Builder() {
        }

        public Builder namePrefix(String namePrefix) {
            this.namePrefix = namePrefix;
            return this;
        }

        /** Max queues per page (server max 1000, default 100). */
        public Builder pageSize(int pageSize) {
            this.pageSize = pageSize;
            return this;
        }

        /** {@code nextPageToken} from the previous page. */
        public Builder pageToken(String pageToken) {
            this.pageToken = pageToken;
            return this;
        }

        public ListQueuesParams build() {
            return new ListQueuesParams(this);
        }
    }
}
