package mybatis

import (
	"strings"
	"testing"
)

const pinXML = `<?xml version="1.0" encoding="UTF-8" ?>
<!DOCTYPE mapper
        PUBLIC "-//mybatis.org//DTD Mapper 3.0//EN"
        "http://mybatis.org/dtd/mybatis-3-mapper.dtd">
<mapper namespace="com.vt.td.repository.PinRepo">
    <sql id="cols">merchant_id, post_id</sql>
    <!-- <select id="old">SELECT 1</select> -->
    <insert id="upsertPinPost" parameterType="com.vt.td.model.entity.Pin">
        INSERT INTO pins (<include refid="cols"/>) VALUES (#{merchantId}, #{postId})
    </insert>
    <select
            id='selectByMerchantIdAndPostId' resultType="com.vt.td.model.entity.Pin">
        SELECT * FROM pins WHERE merchant_id = #{merchantId} AND deleted_at &lt; NOW()
    </select>
</mapper>
`

func TestParse(t *testing.T) {
	m, ok := Parse([]byte(pinXML))
	if !ok || m.Namespace != "com.vt.td.repository.PinRepo" || pinXML[m.Start:m.End] != m.Namespace {
		t.Fatalf("mapper %+v, %v", m, ok)
	}
	var ids []string
	for _, s := range m.Statements {
		if pinXML[s.Start:s.End] != s.ID {
			t.Errorf("%s spans %q", s.ID, pinXML[s.Start:s.End])
		}
		ids = append(ids, s.ID)
	}
	// Not the sql piece, nor the commented out select.
	if got := strings.Join(ids, " "); got != "upsertPinPost selectByMerchantIdAndPostId" {
		t.Fatalf("statements %q", got)
	}
	latin := `<?xml version="1.0" encoding="ISO-8859-1"?>` + "\n" + `<mapper namespace="a.B"><update id="c">UPDATE t SET n = 'caf` + "\xe9" + `'</update></mapper>`
	if m, ok := Parse([]byte(latin)); !ok || len(m.Statements) != 1 || latin[m.Statements[0].Start:m.Statements[0].End] != "c" {
		t.Errorf("ISO-8859-1 mapper %+v, %v", m, ok)
	}
	if _, ok := Parse([]byte(`<configuration><mappers/></configuration>`)); ok {
		t.Fatal("a config parsed as a mapper")
	}
}

const pinJava = `package com.vt.td.repository;

import com.vt.td.model.entity.Pin;

@Mapper
public interface PinRepo {
    /** Calls upsertPinPost(pin) once. */
    int upsertPinPost(Pin pin);
    Pin selectByMerchantIdAndPostId(
            @Param("merchantId") UUID merchantId,
            @Param("postId") UUID postId
    );
    default int twice(Pin pin) { return upsertPinPost(pin) + this.upsertPinPost(pin); }
}
`

func TestJavaSide(t *testing.T) {
	if got := TypeName("/p/src/main/java/com/vt/td/repository/PinRepo.java", []byte(pinJava)); got != "com.vt.td.repository.PinRepo" {
		t.Errorf("type name %q", got)
	}
	files := []string{"pom.xml", "src/main/java/com/vt/td/model/PinRepo.java", "src/main/java/com/vt/td/repository/PinRepo.java"}
	if got, ok := JavaFile(files, "com.vt.td.repository.PinRepo"); !ok || got != files[2] {
		t.Errorf("java file %q, %v", got, ok)
	}
	if got := Method([]byte(pinJava), "upsertPinPost"); got != strings.Index(pinJava, "upsertPinPost(Pin pin);") {
		t.Errorf("method at %d", got)
	}
	if got := Method([]byte(pinJava), "selectByMerchantIdAndPostId"); got != strings.Index(pinJava, "selectByMerchantIdAndPostId") {
		t.Errorf("multi-line method at %d", got)
	}
	if got := Type([]byte(pinJava), "PinRepo"); got != strings.Index(pinJava, "PinRepo {") {
		t.Errorf("type at %d", got)
	}
	if Method([]byte(pinJava), "missing") != -1 {
		t.Error("found a missing method")
	}
}
