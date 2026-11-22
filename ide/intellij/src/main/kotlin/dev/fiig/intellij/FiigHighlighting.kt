package dev.fiig.intellij

import com.intellij.lexer.Lexer
import com.intellij.openapi.editor.DefaultLanguageHighlighterColors as D
import com.intellij.openapi.editor.HighlighterColors
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.editor.colors.TextAttributesKey.createTextAttributesKey
import com.intellij.openapi.fileTypes.SyntaxHighlighter
import com.intellij.openapi.fileTypes.SyntaxHighlighterBase
import com.intellij.openapi.fileTypes.SyntaxHighlighterFactory
import com.intellij.openapi.options.colors.AttributesDescriptor
import com.intellij.openapi.options.colors.ColorDescriptor
import com.intellij.openapi.options.colors.ColorSettingsPage
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.psi.tree.IElementType
import javax.swing.Icon

/** ADR 0090 §2: highlighting by token kind, each color a user may change. */
object FiigColors {
    val KEYWORD = createTextAttributesKey("FIIG_KEYWORD", D.KEYWORD)
    val IDENTIFIER = createTextAttributesKey("FIIG_IDENTIFIER", D.IDENTIFIER)
    val TYPE_NAME = createTextAttributesKey("FIIG_TYPE_NAME", D.CLASS_NAME)
    val NUMBER = createTextAttributesKey("FIIG_NUMBER", D.NUMBER)
    val STRING = createTextAttributesKey("FIIG_STRING", D.STRING)
    val ESCAPE = createTextAttributesKey("FIIG_ESCAPE", D.VALID_STRING_ESCAPE)
    val INVALID_ESCAPE = createTextAttributesKey("FIIG_INVALID_ESCAPE", D.INVALID_STRING_ESCAPE)
    val INTERPOLATION = createTextAttributesKey("FIIG_INTERPOLATION", D.VALID_STRING_ESCAPE)
    val COMMENT = createTextAttributesKey("FIIG_COMMENT", D.LINE_COMMENT)
    val BRACES = createTextAttributesKey("FIIG_BRACES", D.BRACES)
    val PARENTHESES = createTextAttributesKey("FIIG_PARENTHESES", D.PARENTHESES)
    val BRACKETS = createTextAttributesKey("FIIG_BRACKETS", D.BRACKETS)
    val OPERATOR = createTextAttributesKey("FIIG_OPERATOR", D.OPERATION_SIGN)
    val COMMA = createTextAttributesKey("FIIG_COMMA", D.COMMA)
    val DOT = createTextAttributesKey("FIIG_DOT", D.DOT)
    val BAD = createTextAttributesKey("FIIG_BAD_CHARACTER", HighlighterColors.BAD_CHARACTER)

    // ADR 0106 §1: a call, coloured by the kind of what it calls, from the
    // language server's semantic tokens. Pure calls and calls with effects
    // start from different colours.
    val RULE_CALL = createTextAttributesKey("FIIG_RULE_CALL", D.FUNCTION_CALL)
    val CONVERSION_CALL = createTextAttributesKey("FIIG_CONVERSION_CALL", D.FUNCTION_CALL)
    val FORMULA_CALL = createTextAttributesKey("FIIG_FORMULA_CALL", D.FUNCTION_CALL)
    val HELPER_CALL = createTextAttributesKey("FIIG_HELPER_CALL", D.FUNCTION_CALL)
    val ACTIVITY_CALL = createTextAttributesKey("FIIG_ACTIVITY_CALL", D.INSTANCE_METHOD)
    val GATEWAY_CALL = createTextAttributesKey("FIIG_GATEWAY_CALL", D.STATIC_METHOD)
    val WORKFLOW_CALL = createTextAttributesKey("FIIG_WORKFLOW_CALL", D.CONSTANT)
    val CAPABILITY_CALL = createTextAttributesKey("FIIG_CAPABILITY_CALL", D.STATIC_METHOD)

    /** The language server's token types (tooling/lsp/semantic.go), to colours. */
    val BY_SEMANTIC_TYPE = mapOf(
        "rule" to RULE_CALL,
        "conversion" to CONVERSION_CALL,
        "formula" to FORMULA_CALL,
        "helper" to HELPER_CALL,
        "activity" to ACTIVITY_CALL,
        "gateway" to GATEWAY_CALL,
        "workflow" to WORKFLOW_CALL,
        "capability" to CAPABILITY_CALL,
    )
}

class FiigSyntaxHighlighter : SyntaxHighlighterBase() {
    override fun getHighlightingLexer(): Lexer = FiigLexer()

    override fun getTokenHighlights(type: IElementType): Array<TextAttributesKey> {
        val key = when (type) {
            FiigTokens.KEYWORD -> FiigColors.KEYWORD
            FiigTokens.IDENTIFIER -> FiigColors.IDENTIFIER
            FiigTokens.TYPE_NAME -> FiigColors.TYPE_NAME
            FiigTokens.NUMBER -> FiigColors.NUMBER
            FiigTokens.STRING -> FiigColors.STRING
            FiigTokens.STRING_ESCAPE -> FiigColors.ESCAPE
            FiigTokens.INVALID_ESCAPE -> FiigColors.INVALID_ESCAPE
            FiigTokens.INTERP_START, FiigTokens.INTERP_END -> FiigColors.INTERPOLATION
            FiigTokens.LINE_COMMENT -> FiigColors.COMMENT
            FiigTokens.LBRACE, FiigTokens.RBRACE -> FiigColors.BRACES
            FiigTokens.LPAREN, FiigTokens.RPAREN -> FiigColors.PARENTHESES
            FiigTokens.LBRACKET, FiigTokens.RBRACKET, FiigTokens.BINARY_OPEN -> FiigColors.BRACKETS
            FiigTokens.OPERATOR, FiigTokens.ARROW, FiigTokens.COLON -> FiigColors.OPERATOR
            FiigTokens.COMMA -> FiigColors.COMMA
            FiigTokens.DOT -> FiigColors.DOT
            FiigTokens.BAD_CHARACTER -> FiigColors.BAD
            else -> return TextAttributesKey.EMPTY_ARRAY
        }
        return arrayOf(key)
    }
}

class FiigSyntaxHighlighterFactory : SyntaxHighlighterFactory() {
    override fun getSyntaxHighlighter(project: Project?, virtualFile: VirtualFile?): SyntaxHighlighter = FiigSyntaxHighlighter()
}

class FiigColorSettingsPage : ColorSettingsPage {
    override fun getIcon(): Icon = FiigIcons.FILE
    override fun getHighlighter(): SyntaxHighlighter = FiigSyntaxHighlighter()
    override fun getDisplayName(): String = "fiig"
    // The demo marks calls with tags, since the page has no language server.
    override fun getAdditionalHighlightingTagToDescriptorMap(): Map<String, TextAttributesKey> =
        FiigColors.BY_SEMANTIC_TYPE.mapKeys { (type, _) -> "${type}Call" }
    override fun getColorDescriptors(): Array<ColorDescriptor> = ColorDescriptor.EMPTY_ARRAY

    override fun getAttributeDescriptors(): Array<AttributesDescriptor> = arrayOf(
        AttributesDescriptor("Keyword", FiigColors.KEYWORD),
        AttributesDescriptor("Identifier", FiigColors.IDENTIFIER),
        AttributesDescriptor("Type or declaration name", FiigColors.TYPE_NAME),
        AttributesDescriptor("Number", FiigColors.NUMBER),
        AttributesDescriptor("String//Text", FiigColors.STRING),
        AttributesDescriptor("String//Escape", FiigColors.ESCAPE),
        AttributesDescriptor("String//Invalid escape", FiigColors.INVALID_ESCAPE),
        AttributesDescriptor("String//Interpolation", FiigColors.INTERPOLATION),
        AttributesDescriptor("Comment", FiigColors.COMMENT),
        AttributesDescriptor("Braces and operators//Braces", FiigColors.BRACES),
        AttributesDescriptor("Braces and operators//Parentheses", FiigColors.PARENTHESES),
        AttributesDescriptor("Braces and operators//Brackets", FiigColors.BRACKETS),
        AttributesDescriptor("Braces and operators//Operator", FiigColors.OPERATOR),
        AttributesDescriptor("Braces and operators//Comma", FiigColors.COMMA),
        AttributesDescriptor("Braces and operators//Dot", FiigColors.DOT),
        AttributesDescriptor("Bad character", FiigColors.BAD),
        AttributesDescriptor("Calls//Rule", FiigColors.RULE_CALL),
        AttributesDescriptor("Calls//Conversion", FiigColors.CONVERSION_CALL),
        AttributesDescriptor("Calls//Formula", FiigColors.FORMULA_CALL),
        AttributesDescriptor("Calls//Private helper", FiigColors.HELPER_CALL),
        AttributesDescriptor("Calls//Activity", FiigColors.ACTIVITY_CALL),
        AttributesDescriptor("Calls//Gateway", FiigColors.GATEWAY_CALL),
        AttributesDescriptor("Calls//Workflow", FiigColors.WORKFLOW_CALL),
        AttributesDescriptor("Calls//Capability operation", FiigColors.CAPABILITY_CALL),
    )

    override fun getDemoText(): String = """
        // A gateway reads a typed row (ADR 0082).
        gateway findRating(item: string) -> Result<Rating, RatingError> requires Database idempotent {
            row: Rating | None = Database.queryOne("SELECT item, score FROM ratings WHERE item = ?", item)
            match row {
                found -> Ok(found)
                None -> Err(RatingError.NotFound(item))
            }
        }

        workflow rate(item: string) -> Result<Rating, RatingError> {
            found = <activityCall>loadRating</activityCall>(item)?
            return <ruleCall>checkRating</ruleCall>(found)
        }

        activity loadRating(item: string) -> Result<Rating, RatingError> {
            return <gatewayCall>findRating</gatewayCall>(item)
        }

        rule label(score: int, weight: decimal) -> string {
            return "score ${'$'}{score} of 10\tweight ${'$'}{weight * 1.5e2}\q"
        }
    """.trimIndent()
}
