package io.pushq;

/** Optional filters and pagination for {@link Pushq#listTasks}. */
public final class ListTasksParams {

    private final String state;
    private final String idPrefix;
    private final String view;
    private final Integer pageSize;
    private final String pageToken;

    private ListTasksParams(Builder b) {
        this.state = b.state;
        this.idPrefix = b.idPrefix;
        this.view = b.view;
        this.pageSize = b.pageSize;
        this.pageToken = b.pageToken;
    }

    public static Builder builder() {
        return new Builder();
    }

    public String getState() {
        return state;
    }

    public String getIdPrefix() {
        return idPrefix;
    }

    public String getView() {
        return view;
    }

    public Integer getPageSize() {
        return pageSize;
    }

    public String getPageToken() {
        return pageToken;
    }

    public static final class Builder {
        private String state;
        private String idPrefix;
        private String view;
        private Integer pageSize;
        private String pageToken;

        private Builder() {
        }

        /** Filter by state: {@code pending}, {@code running}, {@code succeeded}, {@code failed}. */
        public Builder state(String state) {
            this.state = state;
            return this;
        }

        /** Only tasks whose ID starts with this prefix. */
        public Builder idPrefix(String idPrefix) {
            this.idPrefix = idPrefix;
            return this;
        }

        /** {@code basic} (default) or {@code full} (includes body_base64). */
        public Builder view(String view) {
            this.view = view;
            return this;
        }

        /** Max tasks per page (server max 1000, default 100). */
        public Builder pageSize(int pageSize) {
            this.pageSize = pageSize;
            return this;
        }

        /** {@code nextPageToken} from the previous page. */
        public Builder pageToken(String pageToken) {
            this.pageToken = pageToken;
            return this;
        }

        public ListTasksParams build() {
            return new ListTasksParams(this);
        }
    }
}
