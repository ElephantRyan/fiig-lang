package dev.fiig.intellij

import com.intellij.lexer.LexerBase
import com.intellij.psi.tree.IElementType

/**
 * ADR 0090 §2: fiig's tokens, as compiler/lexer reads them: identifiers,
 * keywords (FiigKeywords, generated from the compiler), numbers, strings with
 * escapes and `${...}` interpolation, `//` comments, and punctuation.
 *
 * The state is how deep the lexer is in strings and interpolations, so an
 * editor restarts it only where that depth is 0.
 */
class FiigLexer : LexerBase() {
    private var buffer: CharSequence = ""
    private var end = 0
    private var tokenStart = 0
    private var tokenEnd = 0
    private var tokenType: IElementType? = null

    // Contexts, innermost last: STRING, or an interpolation's brace depth
    // (a value >= 0).
    private val stack = ArrayList<Int>()

    override fun start(buffer: CharSequence, startOffset: Int, endOffset: Int, initialState: Int) {
        this.buffer = buffer
        this.end = endOffset
        stack.clear()
        tokenEnd = startOffset
        advance()
    }

    override fun getState(): Int = stack.size
    override fun getTokenType(): IElementType? = tokenType
    override fun getTokenStart(): Int = tokenStart
    override fun getTokenEnd(): Int = tokenEnd
    override fun getBufferSequence(): CharSequence = buffer
    override fun getBufferEnd(): Int = end

    override fun advance() {
        tokenStart = tokenEnd
        if (tokenStart >= end) {
            tokenType = null
            return
        }
        if (stack.isNotEmpty() && stack.last() == STRING) {
            stringPart()
        } else {
            code()
        }
    }

    private fun at(i: Int): Char = if (i < end) buffer[i] else '\u0000'

    private fun code() {
        var i = tokenStart
        val c = at(i)
        when {
            c == ' ' || c == '\t' || c == '\r' || c == '\n' -> {
                while (i < end && at(i).let { it == ' ' || it == '\t' || it == '\r' || it == '\n' }) i++
                emit(i, FiigTokens.WHITE_SPACE)
            }
            c == '/' && at(i + 1) == '/' -> {
                while (i < end && at(i) != '\n') i++
                emit(i, FiigTokens.LINE_COMMENT)
            }
            c == '"' -> {
                stack.add(STRING)
                emit(i + 1, FiigTokens.STRING)
            }
            isLetter(c) -> {
                while (i < end && isIdentContinue(at(i))) i++
                val word = buffer.subSequence(tokenStart, i).toString()
                emit(i, when {
                    word in FiigKeywords.ALL -> FiigTokens.KEYWORD
                    word[0].isUpperCase() -> FiigTokens.TYPE_NAME
                    else -> FiigTokens.IDENTIFIER
                })
            }
            c in '0'..'9' -> emit(number(i), FiigTokens.NUMBER)
            c == '_' -> emit(i + 1, FiigTokens.UNDERSCORE)
            else -> punctuation(i, c)
        }
    }

    private fun number(start: Int): Int {
        var i = start
        if (at(i) == '0' && (at(i + 1) == 'x' || at(i + 1) == 'X')) {
            i += 2
            while (at(i).isHexDigit()) i++
            return i
        }
        while (at(i) in '0'..'9') i++
        if (at(i) == '.' && at(i + 1) in '0'..'9') {
            i++
            while (at(i) in '0'..'9') i++
        }
        if (at(i) == 'e' || at(i) == 'E') {
            var j = i + 1
            if (at(j) == '+' || at(j) == '-') j++
            if (at(j) in '0'..'9') {
                i = j
                while (at(i) in '0'..'9') i++
            }
        }
        return i
    }

    private fun punctuation(i: Int, c: Char) {
        val two = if (i + 1 < end) "" + c + at(i + 1) else ""
        when (two) {
            "->" -> return emit(i + 2, FiigTokens.ARROW)
            "<<" -> return emit(i + 2, FiigTokens.BINARY_OPEN)
            "==", "!=", "<=", ">=" -> return emit(i + 2, FiigTokens.OPERATOR)
        }
        val type = when (c) {
            '{' -> {
                if (stack.isNotEmpty()) stack[stack.size - 1] = stack.last() + 1
                FiigTokens.LBRACE
            }
            '}' -> {
                if (stack.isNotEmpty()) {
                    if (stack.last() == 0) {
                        stack.removeAt(stack.size - 1)
                        return emit(i + 1, FiigTokens.INTERP_END)
                    }
                    stack[stack.size - 1] = stack.last() - 1
                }
                FiigTokens.RBRACE
            }
            '(' -> FiigTokens.LPAREN
            ')' -> FiigTokens.RPAREN
            '[' -> FiigTokens.LBRACKET
            ']' -> FiigTokens.RBRACKET
            ',' -> FiigTokens.COMMA
            '.' -> FiigTokens.DOT
            ':' -> FiigTokens.COLON
            '<', '>', '?', '=', '|', '*', '+', '-', '/', '%' -> FiigTokens.OPERATOR
            else -> FiigTokens.BAD_CHARACTER
        }
        emit(i + 1, type)
    }

    private fun stringPart() {
        var i = tokenStart
        when {
            at(i) == '"' -> {
                stack.removeAt(stack.size - 1)
                return emit(i + 1, FiigTokens.STRING)
            }
            at(i) == '$' && at(i + 1) == '{' -> {
                stack.add(0)
                return emit(i + 2, FiigTokens.INTERP_START)
            }
            at(i) == '\\' -> {
                if (i + 1 >= end) return emit(i + 1, FiigTokens.INVALID_ESCAPE)
                val valid = at(i + 1) in "\"\\ntr$"
                return emit(i + 2, if (valid) FiigTokens.STRING_ESCAPE else FiigTokens.INVALID_ESCAPE)
            }
        }
        while (i < end && at(i) != '"' && at(i) != '\\' && !(at(i) == '$' && at(i + 1) == '{')) i++
        emit(i, FiigTokens.STRING)
    }

    private fun emit(to: Int, type: IElementType) {
        tokenEnd = minOf(to, end)
        tokenType = type
    }

    private companion object {
        const val STRING = -1

        fun isLetter(c: Char) = c in 'a'..'z' || c in 'A'..'Z'
        fun isIdentContinue(c: Char) = isLetter(c) || c in '0'..'9' || c == '_'
        fun Char.isHexDigit() = this in '0'..'9' || this in 'a'..'f' || this in 'A'..'F'
    }
}
