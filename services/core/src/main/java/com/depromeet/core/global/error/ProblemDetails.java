package com.depromeet.core.global.error;

import org.springframework.http.HttpStatusCode;
import org.springframework.http.ProblemDetail;

/**
 * Problem JSON(RFC 9457)에 {@code code}를 붙인다.
 */
public final class ProblemDetails {

	public static final String CODE = "code";

	private ProblemDetails() {}

	public static ProblemDetail of(ErrorCode errorCode, String detail) {
		return of(errorCode.status(), errorCode, detail);
	}

	public static ProblemDetail of(HttpStatusCode status, ErrorCode errorCode, String detail) {
		ProblemDetail problem = ProblemDetail.forStatusAndDetail(status, detail);
		problem.setProperty(CODE, errorCode.name());
		return problem;
	}
}
