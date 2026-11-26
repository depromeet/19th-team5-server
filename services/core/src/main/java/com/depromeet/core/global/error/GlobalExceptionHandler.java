package com.depromeet.core.global.error;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatusCode;
import org.springframework.http.ProblemDetail;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.RestControllerAdvice;
import org.springframework.web.context.request.WebRequest;
import org.springframework.web.servlet.mvc.method.annotation.ResponseEntityExceptionHandler;

/**
 * 모든 에러를 Problem JSON + {@code code}로 응답한다.
 * Spring MVC 기본 예외(잘못된 JSON, 검증 실패 등)는 부모 클래스가 ProblemDetail을 만들고,
 * 여기서 {@code code}만 붙인다.
 */
@RestControllerAdvice
public class GlobalExceptionHandler extends ResponseEntityExceptionHandler {

	private static final Logger log = LoggerFactory.getLogger(GlobalExceptionHandler.class);

	@ExceptionHandler(BusinessException.class)
	public ProblemDetail handleBusiness(BusinessException e) {
		return ProblemDetails.of(e.errorCode(), e.getMessage());
	}

	@ExceptionHandler(Exception.class)
	public ProblemDetail handleUnexpected(Exception e) {
		log.error("처리하지 못한 예외", e);
		return ProblemDetails.of(ErrorCode.INTERNAL_ERROR, ErrorCode.INTERNAL_ERROR.message());
	}

	// handleExceptionInternal은 body가 null로 들어오고 ProblemDetail은 그 안에서 만들어진다.
	// 응답 직전인 여기서 code를 붙인다.
	@Override
	protected ResponseEntity<Object> createResponseEntity(
		Object body, HttpHeaders headers, HttpStatusCode status, WebRequest request) {
		if (body instanceof ProblemDetail problem
			&& (problem.getProperties() == null || !problem.getProperties().containsKey(ProblemDetails.CODE))) {
			problem.setProperty(ProblemDetails.CODE, codeFor(status).name());
		}
		return super.createResponseEntity(body, headers, status, request);
	}

	private static ErrorCode codeFor(HttpStatusCode status) {
		return switch (status.value()) {
			case 400 -> ErrorCode.INVALID_INPUT;
			case 401 -> ErrorCode.UNAUTHORIZED;
			case 403 -> ErrorCode.FORBIDDEN;
			case 404 -> ErrorCode.NOT_FOUND;
			default -> status.is5xxServerError() ? ErrorCode.INTERNAL_ERROR : ErrorCode.INVALID_INPUT;
		};
	}
}
