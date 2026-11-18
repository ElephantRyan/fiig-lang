package dev.fiig.intellij

import com.intellij.psi.TokenType
import com.intellij.psi.tree.IElementType
import com.intellij.psi.tree.TokenSet

class FiigTokenType(debugName: String) : IElementType(debugName, FiigLanguage) {
    override fun toString(): String = "FiigToken." + super.toString()
}

/** The token kinds the plugin's lexer produces, mirroring compiler/lexer's. */
object FiigTokens {
    @JvmField val KEYWORD = FiigTokenType("KEYWORD")
    @JvmField val IDENTIFIER = FiigTokenType("IDENTIFIER")
    @JvmField val TYPE_NAME = FiigTokenType("TYPE_NAME")
    @JvmField val NUMBER = FiigTokenType("NUMBER")
    @JvmField val STRING = FiigTokenType("STRING")
    @JvmField val STRING_ESCAPE = FiigTokenType("STRING_ESCAPE")
    @JvmField val INVALID_ESCAPE = FiigTokenType("INVALID_ESCAPE")
    @JvmField val INTERP_START = FiigTokenType("INTERP_START")
    @JvmField val INTERP_END = FiigTokenType("INTERP_END")
    @JvmField val LINE_COMMENT = FiigTokenType("LINE_COMMENT")
    @JvmField val LBRACE = FiigTokenType("LBRACE")
    @JvmField val RBRACE = FiigTokenType("RBRACE")
    @JvmField val LPAREN = FiigTokenType("LPAREN")
    @JvmField val RPAREN = FiigTokenType("RPAREN")
    @JvmField val LBRACKET = FiigTokenType("LBRACKET")
    @JvmField val RBRACKET = FiigTokenType("RBRACKET")
    @JvmField val BINARY_OPEN = FiigTokenType("BINARY_OPEN")
    @JvmField val COMMA = FiigTokenType("COMMA")
    @JvmField val DOT = FiigTokenType("DOT")
    @JvmField val COLON = FiigTokenType("COLON")
    @JvmField val ARROW = FiigTokenType("ARROW")
    @JvmField val OPERATOR = FiigTokenType("OPERATOR")
    @JvmField val UNDERSCORE = FiigTokenType("UNDERSCORE")

    @JvmField val WHITE_SPACE: IElementType = TokenType.WHITE_SPACE
    @JvmField val BAD_CHARACTER: IElementType = TokenType.BAD_CHARACTER

    @JvmField val COMMENTS = TokenSet.create(LINE_COMMENT)
    @JvmField val STRINGS = TokenSet.create(STRING, STRING_ESCAPE, INVALID_ESCAPE)
    @JvmField val WHITESPACES = TokenSet.create(TokenType.WHITE_SPACE)
}
