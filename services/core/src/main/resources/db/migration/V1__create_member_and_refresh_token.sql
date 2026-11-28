-- 회원
CREATE TABLE member (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- 소문자로 바꿔 저장한다. 같은 주소를 대소문자만 바꿔 두 번 가입하지 못하게 한다
    email         VARCHAR(254) NOT NULL,
    -- {bcrypt}$2a$10$... 처럼 알고리즘 이름표를 붙인다 (ADR-0012). 알고리즘이 바뀌어도 들어가게 넉넉히 잡는다
    password_hash VARCHAR(255) NOT NULL,
    role          VARCHAR(20)  NOT NULL,
    status        VARCHAR(20)  NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL,
    updated_at    TIMESTAMPTZ  NOT NULL,
    CONSTRAINT uk_member_email UNIQUE (email),
    CONSTRAINT ck_member_email_lower CHECK (email = lower(email)),
    CONSTRAINT ck_member_role CHECK (role IN ('MEMBER', 'ADMIN')),
    CONSTRAINT ck_member_status CHECK (status IN ('ACTIVE', 'RESTRICTED', 'WITHDRAWN'))
);

-- 리프레시 토큰 묶음. 로그인 1번 = 묶음 1개 (ADR-0004)
CREATE TABLE refresh_token_family (
    id         UUID PRIMARY KEY,
    member_id  BIGINT      NOT NULL REFERENCES member (id),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_refresh_token_family_member_id ON refresh_token_family (member_id);

-- 리프레시 토큰. 원문은 저장하지 않고 SHA-256 해시(16진수 64자)만 저장한다
CREATE TABLE refresh_token (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    family_id     UUID        NOT NULL REFERENCES refresh_token_family (id),
    member_id     BIGINT      NOT NULL REFERENCES member (id),
    token_hash    VARCHAR(64) NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ,
    revoke_reason VARCHAR(20),
    CONSTRAINT uk_refresh_token_token_hash UNIQUE (token_hash),
    CONSTRAINT ck_refresh_token_revoke_reason
        CHECK (revoke_reason IN ('ROTATED', 'LOGOUT', 'REUSE_DETECTED', 'PASSWORD_CHANGED', 'WITHDRAWN')),
    -- 폐기 시각과 사유는 함께 있거나 함께 없다
    CONSTRAINT ck_refresh_token_revoked CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL))
);

CREATE INDEX idx_refresh_token_family_id ON refresh_token (family_id);
CREATE INDEX idx_refresh_token_member_id ON refresh_token (member_id);
