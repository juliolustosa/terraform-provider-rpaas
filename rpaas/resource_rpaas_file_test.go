// Copyright 2021 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpaas

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"

	echo "github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tsuru/rpaas-operator/pkg/rpaas/client/types"
)

func TestAccRpaasFile_basic(t *testing.T) {
	setupFakeServerRpaasFile(t)

	resourceName := "rpaas_file.custom_file"
	importedResourceName := "rpaas_file.imported"

	resource.Test(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		IDRefreshName:     resourceName,
		ProviderFactories: testAccProviderFactories,
		Steps: []resource.TestStep{
			{
				// Testing Create
				Config: `
resource "rpaas_file" "custom_file" {
	instance     = "my_rpaas"
	service_name = "rpaasv2-be"
	name         = "custom_file.txt"
	content      = <<-EOF
		line1
		line2
	EOF
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccResourceExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "instance", "my_rpaas"),
					resource.TestCheckResourceAttr(resourceName, "service_name", "rpaasv2-be"),
					resource.TestCheckResourceAttr(resourceName, "name", "custom_file.txt"),
					resource.TestCheckResourceAttr(resourceName, "content", "line1\nline2\n"),
				),
			},
			{
				// Testing Import
				Config:        `resource "rpaas_file" "imported" {}`,
				ResourceName:  importedResourceName,
				ImportStateId: "rpaasv2-be/my_rpaas/import_file.txt",
				ImportState:   true,
				ImportStateCheck: func(s []*terraform.InstanceState) error {
					state := s[0]
					assert.Equal(t, "rpaasv2-be", state.Attributes["service_name"])
					assert.Equal(t, "my_rpaas", state.Attributes["instance"])
					assert.Equal(t, "import_file.txt", state.Attributes["name"])
					assert.Equal(t, "imported", state.Attributes["content"])
					return nil
				},
			},
			{
				// Testing Update
				Config: terraformConfigRpaasFileResource("custom_file", "custom_file.txt", "changed"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccResourceExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "instance", "my_rpaas"),
					resource.TestCheckResourceAttr(resourceName, "service_name", "rpaasv2-be"),
					resource.TestCheckResourceAttr(resourceName, "name", "custom_file.txt"),
					resource.TestCheckResourceAttr(resourceName, "content", "changed"),
				),
			},
		},
	})
}

func setupFakeServerRpaasFile(t *testing.T) {
	resourceWasUpdatesd := false
	fakeServer := echo.New()

	fakeServer.POST("/services/rpaasv2-be/proxy/my_rpaas", func(c echo.Context) error {
		uploadedFiles, err := parseFileMapFromContext(c)
		if err != nil {
			t.Error(err)
		}
		assert.Equal(t, map[string]string{
			"custom_file.txt": "line1\nline2\n",
		}, uploadedFiles)

		return c.NoContent(http.StatusCreated)
	})

	fakeServer.GET("/services/rpaasv2-be/proxy/my_rpaas", func(c echo.Context) error {
		qparam := c.Request().URL.Query()
		path := qparam["callback"][0]
		if path == "/resources/my_rpaas/files/custom_file.txt" {
			content := []byte("line1\nline2\n")
			if resourceWasUpdatesd {
				content = []byte("changed")
			}

			return c.JSON(http.StatusOK, types.RpaasFile{
				Name:    "custom_file.txt",
				Content: content,
			})
		} else if path == "/resources/my_rpaas/files/import_file.txt" {
			return c.JSON(http.StatusOK, types.RpaasFile{
				Name:    "import_file.txt",
				Content: []byte("imported"),
			})
		}
		return c.NoContent(http.StatusNotFound)
	})

	fakeServer.PUT("/services/rpaasv2-be/proxy/my_rpaas", func(c echo.Context) error {
		resourceWasUpdatesd = true // So GET returns a different value
		uploadedFiles, err := parseFileMapFromContext(c)
		if err != nil {
			t.Error(err)
		}
		assert.Equal(t, map[string]string{
			"custom_file.txt": "changed",
		}, uploadedFiles)

		return c.NoContent(http.StatusOK)
	})

	fakeServer.DELETE("/services/rpaasv2-be/proxy/my_rpaas", func(c echo.Context) error {
		p := []string{}
		err := json.NewDecoder(c.Request().Body).Decode(&p)
		require.NoError(t, err)
		assert.Len(t, p, 1)
		assert.Equal(t, "custom_file.txt", p[0])

		return c.NoContent(http.StatusOK)
	})

	fakeServer.HTTPErrorHandler = func(err error, c echo.Context) {
		t.Errorf("methods=%s, path=%s, err=%s", c.Request().Method, c.Path(), err.Error())
	}
	server := httptest.NewServer(fakeServer)

	os.Setenv("TSURU_TARGET", server.URL)
	os.Setenv("TSURU_TOKEN", "asdf")
}

func TestAccRpaasFile_FileNameValidation(t *testing.T) {
	fakeServer := echo.New()
	fakeServer.POST("/services/rpaasv2-be/proxy/my_rpaas", func(c echo.Context) error {
		return c.JSON(http.StatusCreated, nil)
	})

	server := httptest.NewServer(fakeServer)
	os.Setenv("TSURU_TARGET", server.URL)
	os.Setenv("TSURU_TOKEN", "asdf")

	terraformTestSteps := []resource.TestStep{}

	reInvalidFilename := regexp.MustCompile("Error: Invalid filename")
	for _, invalidFilename := range []string{
		"arquivo com espaco.txt",
		"çedilha",
		"outros+caracteres.txt",
		"inválido",
		"*nao*",
		"()",
		"",
	} {
		terraformTestSteps = append(terraformTestSteps, resource.TestStep{
			Config:      terraformConfigRpaasFileResource("resource", invalidFilename, "content"),
			ExpectError: reInvalidFilename,
		})
	}

	for _, validFilename := range []string{
		"arquivoCamelCase.txt",
		"pontos.pode.ser.txt....e.nao.e..muito...exigente....",
		"sim_-.",
	} {
		terraformTestSteps = append(terraformTestSteps, resource.TestStep{
			Config:             terraformConfigRpaasFileResource("resource", validFilename, "content"),
			ExpectNonEmptyPlan: true,
			PlanOnly:           true,
		})
	}

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:          func() { testAccPreCheck(t) },
		ProviderFactories: testAccProviderFactories,
		Steps:             terraformTestSteps,
		CheckDestroy:      nil,
	})

}

func parseFileMapFromContext(c echo.Context) (map[string]string, error) {
	mediaType, params, err := mime.ParseMediaType(c.Request().Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}
	if mediaType != "multipart/form-data" {
		return nil, fmt.Errorf("Content-Type was not multipart/form-data. Got %q instead", mediaType)
	}

	mr := multipart.NewReader(c.Request().Body, params["boundary"])
	uploadedFiles := map[string]string{}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return uploadedFiles, err
		}
		slurp, err := io.ReadAll(p)
		if err != nil {
			return uploadedFiles, err
		}
		uploadedFiles[p.FileName()] = string(slurp)
	}
	return uploadedFiles, nil
}

func terraformConfigRpaasFileResource(resourceName, filename, content string) string {
	return fmt.Sprintf(`
resource "rpaas_file" "%s" {
	instance     = "my_rpaas"
	service_name = "rpaasv2-be"
	name         = "%s"
	content      = "%s"
}
`, resourceName, filename, content)
}