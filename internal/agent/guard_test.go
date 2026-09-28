package agent

import (
	"strings"
	"testing"
)

func TestRepeatedFragmentCatchesDegenerateEcho(t *testing.T) {
	frag := "La respuesta es siempre la misma, una y otra vez. "
	reply := strings.Repeat(frag, 6)
	got, share := repeatedFragment(reply)
	if got == "" {
		t.Fatal("degenerate echo not caught")
	}
	if share < 0.9 {
		t.Errorf("share should be ~1.0, got %.2f", share)
	}
}

func TestRepeatedFragmentIgnoresHealthyReplies(t *testing.T) {
	healthy := []string{
		"La capital de Portugal es Lisboa.",
		// A chorus repeated three times is style, not degeneration:
		// it covers far less than half the reply.
		"Estrofa larga con contenido variado y explicaciones que ocupan la mayor parte del texto de la respuesta, con detalle. " +
			"¡Viva! " +
			"Más contenido distinto, con razonamiento y ejemplos concretos que llenan párrafos enteros de información útil. " +
			"¡Viva! " +
			"Y una conclusión también larga, distinta de las anteriores, que cierra la respuesta con datos y matices. " +
			"¡Viva!",
		// A fragment repeated twice stays under the threshold.
		"primera mención del dato concreto aquí; " +
			"segunda mención del dato concreto aquí; " +
			"el resto es desarrollo variado: análisis, ejemplos y matices " +
			"que no se repiten en absoluto a lo largo de toda la respuesta.",
	}
	for _, r := range healthy {
		if frag, share := repeatedFragment(r); frag != "" {
			t.Errorf("healthy reply flagged (share %.2f): %.40q...", share, r)
		}
	}
}
