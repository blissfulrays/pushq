package io.pushq;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.SerializationFeature;
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule;

/**
 * Shared, pre-configured Jackson {@link ObjectMapper} for the SDK.
 *
 * <ul>
 *   <li>Unknown JSON properties are ignored (forward compatibility).</li>
 *   <li>{@code java.time.Instant} is written as ISO-8601 / RFC 3339 strings.</li>
 *   <li>{@code null} fields are omitted from request bodies.</li>
 * </ul>
 */
final class Json {

    static final ObjectMapper MAPPER = new ObjectMapper()
            .registerModule(new JavaTimeModule())
            .disable(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES)
            .disable(SerializationFeature.WRITE_DATES_AS_TIMESTAMPS)
            .setSerializationInclusion(JsonInclude.Include.NON_NULL);

    private Json() {
    }
}
