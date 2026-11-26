package com.depromeet.core.global.error;

import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.content;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.webmvc.test.autoconfigure.WebMvcTest;
import org.springframework.context.annotation.Import;
import org.springframework.http.MediaType;
import org.springframework.security.test.context.support.WithMockUser;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;

import com.depromeet.core.global.security.SecurityConfig;

@WebMvcTest(controllers = GlobalExceptionHandlerTest.ErrorTestController.class)
@Import({SecurityConfig.class, GlobalExceptionHandler.class, GlobalExceptionHandlerTest.ErrorTestController.class})
@WithMockUser
class GlobalExceptionHandlerTest {

	@Autowired
	MockMvc mockMvc;

	@Test
	void 비즈니스_예외는_ErrorCode의_상태와_code로_응답한다() throws Exception {
		mockMvc.perform(get("/test/business"))
			.andExpect(status().isForbidden())
			.andExpect(content().contentTypeCompatibleWith(MediaType.APPLICATION_PROBLEM_JSON))
			.andExpect(jsonPath("$.status").value(403))
			.andExpect(jsonPath("$.code").value("FORBIDDEN"))
			.andExpect(jsonPath("$.detail").value("다른 사람의 예약입니다."));
	}

	@Test
	void 잘못된_JSON은_400_INVALID_INPUT으로_응답한다() throws Exception {
		mockMvc.perform(post("/test/body").contentType(MediaType.APPLICATION_JSON).content("{"))
			.andExpect(status().isBadRequest())
			.andExpect(jsonPath("$.code").value("INVALID_INPUT"));
	}

	@Test
	void 예상하지_못한_예외는_500_INTERNAL_ERROR로_응답하고_내부_메시지를_숨긴다() throws Exception {
		mockMvc.perform(get("/test/unexpected"))
			.andExpect(status().isInternalServerError())
			.andExpect(jsonPath("$.code").value("INTERNAL_ERROR"))
			.andExpect(jsonPath("$.detail").value(ErrorCode.INTERNAL_ERROR.message()));
	}

	@Test
	void 없는_경로는_404_NOT_FOUND로_응답한다() throws Exception {
		mockMvc.perform(get("/test/nothing"))
			.andExpect(status().isNotFound())
			.andExpect(jsonPath("$.code").value("NOT_FOUND"));
	}

	@RestController
	static class ErrorTestController {

		@GetMapping("/test/business")
		void business() {
			throw new BusinessException(ErrorCode.FORBIDDEN, "다른 사람의 예약입니다.");
		}

		@PostMapping("/test/body")
		void body(
			@RequestBody
			Payload payload) {}

		@GetMapping("/test/unexpected")
		void unexpected() {
			throw new IllegalStateException("DB 비밀번호가 틀렸습니다");
		}

		record Payload(String value) {
		}
	}
}
