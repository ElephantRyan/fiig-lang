package dev.fiig.intellij

import com.intellij.psi.tree.IElementType
import junit.framework.TestCase
import java.io.File

/** ADR 0090 §7: the lexer over every `.fiig` file in the repository's examples. */
class FiigLexerTest : TestCase() {
    private fun tokens(text: String): List<Pair<IElementType, String>> {
        val lexer = FiigLexer()
        lexer.start(text)
        val out = ArrayList<Pair<IElementType, String>>()
        var expected = 0
        while (true) {
            val type = lexer.tokenType ?: break
            assertEquals("tokens must be contiguous", expected, lexer.tokenStart)
            assertTrue("token must not be empty at ${lexer.tokenStart}", lexer.tokenEnd > lexer.tokenStart)
            out += type to text.substring(lexer.tokenStart, lexer.tokenEnd)
            expected = lexer.tokenEnd
            lexer.advance()
        }
        assertEquals("tokens must cover the text", text.length, expected)
        return out
    }

    fun testEveryExampleLexes() {
        val root = File(System.getProperty("fiig.examples") ?: "../../examples")
        val files = root.walkTopDown().filter { it.isFile && it.extension == "fiig" }.toList()
        assertTrue("found ${files.size} example files under $root", files.size > 100)
        for (file in files) {
            val text = file.readText()
            val bad = tokens(text).filter { it.first == FiigTokens.BAD_CHARACTER || it.first == FiigTokens.INVALID_ESCAPE }
            assertTrue("${file.path}: $bad", bad.isEmpty())
        }
    }

    fun testStringsEscapesAndInterpolation() {
        val got = tokens("\"a\\n\${x + \"y\"}\\q\"")
        assertEquals(
            listOf(
                FiigTokens.STRING to "\"", FiigTokens.STRING to "a", FiigTokens.STRING_ESCAPE to "\\n",
                FiigTokens.INTERP_START to "\${", FiigTokens.IDENTIFIER to "x", FiigTokens.WHITE_SPACE to " ",
                FiigTokens.OPERATOR to "+", FiigTokens.WHITE_SPACE to " ",
                FiigTokens.STRING to "\"", FiigTokens.STRING to "y", FiigTokens.STRING to "\"",
                FiigTokens.INTERP_END to "}", FiigTokens.INVALID_ESCAPE to "\\q", FiigTokens.STRING to "\"",
            ),
            got,
        )
    }

    fun testWordsNumbersAndPunctuation() {
        val got = tokens("gateway findRating(n: int) -> Rating { x = 0x1F + 2.5e3 // note\n<<len: i32be>> _ }")
            .filter { it.first != FiigTokens.WHITE_SPACE }
        assertEquals(FiigTokens.KEYWORD to "gateway", got[0])
        assertEquals(FiigTokens.IDENTIFIER to "findRating", got[1])
        assertTrue(FiigTokens.TYPE_NAME to "Rating" in got)
        assertTrue(FiigTokens.ARROW to "->" in got)
        assertTrue(FiigTokens.NUMBER to "0x1F" in got)
        assertTrue(FiigTokens.NUMBER to "2.5e3" in got)
        assertTrue(FiigTokens.LINE_COMMENT to "// note" in got)
        assertTrue(FiigTokens.BINARY_OPEN to "<<" in got)
        assertTrue(FiigTokens.UNDERSCORE to "_" in got)
    }

    fun testKeywordsAreTheCompilers() {
        for (word in listOf("model", "workflow", "transaction", "retry", "attempts", "formulas", "config")) {
            assertEquals(word, FiigTokens.KEYWORD, tokens(word).single().first)
        }
        // Contextual words read as identifiers, as the compiler's lexer reads them.
        assertEquals(FiigTokens.IDENTIFIER, tokens("serializable").single().first)
    }

    fun testUnterminatedStringStaysInsideTheText() {
        tokens("rule a() -> string {\n    return \"never closed\n}\n")
        tokens("\"\${")
        tokens("\"\\")
    }
}
